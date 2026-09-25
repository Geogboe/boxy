package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestRun_LiveDownloadMatchesPinnedHash is opt-in (set BOXY_TEST_LIVE_WINTUN=1)
// and hits the real network: it downloads the actual pinned wintun-<version>.zip
// from wintun.net and stages it for real, proving wintunVersion/wintunSHA256
// still point at a real, currently-published release -- the same "does the
// pin still resolve" check task release:snapshot exercises as part of a full
// release dry run, but runnable on its own without invoking GoReleaser.
// Skipped by default so `go test ./...` and CI stay network-independent.
func TestRun_LiveDownloadMatchesPinnedHash(t *testing.T) {
	if os.Getenv("BOXY_TEST_LIVE_WINTUN") != "1" {
		t.Skip("set BOXY_TEST_LIVE_WINTUN=1 to run the live wintun.net download test")
	}
	outDir := t.TempDir()
	if err := run(outDir, []string{"amd64", "arm64"}, download); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		if info, err := os.Stat(filepath.Join(outDir, arch, "wintun.dll")); err != nil || info.Size() == 0 {
			t.Fatalf("expected a non-empty staged %s/wintun.dll, stat = %v, %v", arch, info, err)
		}
	}
}

// buildFixtureZip returns a minimal in-memory zip shaped like a real
// wintun-<version>.zip (wintun/LICENSE.txt plus wintun/bin/<arch>/wintun.dll
// for each of archs), so tests never need the real download or a fixture
// file checked into the repo.
func buildFixtureZip(t *testing.T, archs ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	license, err := zw.Create("wintun/LICENSE.txt")
	if err != nil {
		t.Fatalf("create license entry: %v", err)
	}
	if _, err := license.Write([]byte("fixture license text")); err != nil {
		t.Fatalf("write license entry: %v", err)
	}

	for _, arch := range archs {
		dll, err := zw.Create("wintun/bin/" + arch + "/wintun.dll")
		if err != nil {
			t.Fatalf("create %s dll entry: %v", arch, err)
		}
		if _, err := dll.Write([]byte("fixture-dll-bytes-" + arch)); err != nil {
			t.Fatalf("write %s dll entry: %v", arch, err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close fixture zip: %v", err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestStage_ExtractsLicenseAndEveryRequestedArch(t *testing.T) {
	fixture := buildFixtureZip(t, "amd64", "arm64")
	outDir := t.TempDir()

	if err := stage(fixture, sha256Hex(fixture), outDir, []string{"amd64", "arm64"}); err != nil {
		t.Fatalf("stage: %v", err)
	}

	license, err := os.ReadFile(filepath.Join(outDir, "LICENSE-wintun.txt"))
	if err != nil {
		t.Fatalf("read staged license: %v", err)
	}
	if string(license) != "fixture license text" {
		t.Fatalf("staged license content = %q, want the fixture's text", license)
	}

	for _, arch := range []string{"amd64", "arm64"} {
		dll, err := os.ReadFile(filepath.Join(outDir, arch, "wintun.dll"))
		if err != nil {
			t.Fatalf("read staged %s dll: %v", arch, err)
		}
		want := "fixture-dll-bytes-" + arch
		if string(dll) != want {
			t.Fatalf("staged %s dll content = %q, want %q", arch, dll, want)
		}
	}
}

func TestStage_OnlyExtractsRequestedArchs(t *testing.T) {
	fixture := buildFixtureZip(t, "amd64", "arm64")
	outDir := t.TempDir()

	if err := stage(fixture, sha256Hex(fixture), outDir, []string{"amd64"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "arm64")); !os.IsNotExist(err) {
		t.Fatalf("expected no arm64 directory to be staged, stat err = %v", err)
	}
}

// TestStage_RejectsHashMismatch is the core integrity guarantee the user
// asked for: a corrupted or substituted download must never be silently
// staged into a release archive.
func TestStage_RejectsHashMismatch(t *testing.T) {
	fixture := buildFixtureZip(t, "amd64")
	outDir := t.TempDir()

	err := stage(fixture, "0000000000000000000000000000000000000000000000000000000000000000", outDir, []string{"amd64"})
	if err == nil {
		t.Fatal("expected an error for a SHA-256 mismatch")
	}
	if _, statErr := os.Stat(outDir); statErr != nil {
		t.Fatalf("outDir should still exist: %v", statErr)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read outDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected nothing staged after a hash mismatch, found %v", entries)
	}
}

func TestStage_UnknownArchFailsFast(t *testing.T) {
	fixture := buildFixtureZip(t, "amd64")
	outDir := t.TempDir()

	err := stage(fixture, sha256Hex(fixture), outDir, []string{"386"})
	if err == nil {
		t.Fatal("expected an error for an unmapped GOARCH")
	}
}

func TestStage_MissingArchInZipFailsFast(t *testing.T) {
	// The zip only has amd64; requesting arm64 must fail rather than
	// silently stage nothing for it.
	fixture := buildFixtureZip(t, "amd64")
	outDir := t.TempDir()

	err := stage(fixture, sha256Hex(fixture), outDir, []string{"arm64"})
	if err == nil {
		t.Fatal("expected an error when the zip has no entry for the requested arch")
	}
}

func TestRun_PropagatesFetchError(t *testing.T) {
	wantErr := errors.New("network unreachable")
	err := run(t.TempDir(), []string{"amd64"}, func() ([]byte, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("run error = %v, want it to wrap %v", err, wantErr)
	}
}
