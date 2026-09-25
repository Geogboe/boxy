// Command wintun-fetch downloads, hash-verifies, and stages the prebuilt
// Wintun driver DLL for the Windows release archive (#379 blocker 6:
// wireguard-go's cross-host mesh overlay needs it present next to
// boxy.exe on Windows, and nothing installs it today).
//
// Invoked as a GoReleaser before-hook (see .goreleaser.yml), not
// go generate: its output doesn't belong in the repo -- it fetches a
// pinned, hash-verified third-party binary fresh on every release build,
// the same way checksums.txt/cosign already treat build artifacts as
// ephemeral rather than committed.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// wintunVersion and wintunSHA256 pin the exact release this tool fetches.
// Both values, and the Authenticode signer identity below, were
// independently verified on 2026-09-25 against a fresh download from
// wintun.net (not copied from any third party): the SHA-256 was
// recomputed locally and matched wintun.net's own published value, and
// every architecture's wintun.dll in the archive checked out as validly
// signed by WireGuard LLC (Get-AuthenticodeSignature Status=Valid,
// SignerCertificate thumbprint DF98E075A012ED8C86FBCF14854B8F9555CB3D45,
// issued by DigiCert EV Code Signing CA). See docs/adr/0022's 2026-09-25
// changelog entry and #379's comments for the full sourcing research
// (why wintun.net's own signed DLL is the only supported distribution
// path, and the license terms permitting this exact bundled-redistribution
// use case). Bump both constants together, deliberately, only after
// repeating that verification against the new version and updating this
// comment's thumbprint/version -- never bump just the version number.
const (
	wintunVersion = "0.14.1"
	wintunSHA256  = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
	wintunBaseURL = "https://www.wintun.net/builds/"
)

// wintunArchs maps a Go GOARCH value (as .goreleaser.yml's windows builds
// use it) to Wintun's own per-architecture directory name inside the zip.
// They happen to already match 1:1 for every GOARCH Boxy currently builds
// for (amd64, arm64), but the mapping is kept explicit rather than assumed
// equal, so a future GOARCH this repo adds (e.g. 386 -> Wintun's "x86")
// fails loudly instead of silently staging an empty or wrong DLL.
var wintunArchs = map[string]string{
	"amd64": "amd64",
	"arm64": "arm64",
}

func main() {
	var outDir, archsFlag string
	flag.StringVar(&outDir, "out", "", "output directory to stage per-arch wintun.dll files and the license into (required)")
	flag.StringVar(&archsFlag, "archs", "amd64,arm64", "comma-separated Go GOARCH values to stage a wintun.dll for")
	flag.Parse()

	if outDir == "" {
		fmt.Fprintln(os.Stderr, "wintun-fetch: -out is required")
		os.Exit(2)
	}

	if err := run(outDir, strings.Split(archsFlag, ","), download); err != nil {
		fmt.Fprintf(os.Stderr, "wintun-fetch: %v\n", err)
		os.Exit(1)
	}
}

// run's fetchZip parameter is download in production; tests substitute a
// fake that returns fixture bytes instead of hitting the real network, the
// same injectable-function pattern used elsewhere in this codebase
// (meshnetProbe, isElevatedFn, svcmgrNewManager) for exactly this reason.
func run(outDir string, archs []string, fetchZip func() ([]byte, error)) error {
	zipBytes, err := fetchZip()
	if err != nil {
		return err
	}
	return stage(zipBytes, wintunSHA256, outDir, archs)
}

// stage verifies zipBytes against wantSHA256 and extracts the license plus
// one wintun.dll per requested arch into outDir. Split out from run so
// tests can exercise the verify/extract logic directly against fixture
// bytes -- including the "hash doesn't match" rejection path -- without
// needing bytes that hash to the real pinned wintunSHA256.
func stage(zipBytes []byte, wantSHA256 string, outDir string, archs []string) error {
	sum := sha256.Sum256(zipBytes)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return fmt.Errorf("SHA-256 mismatch for wintun-%s.zip: got %s, want %s (refusing to stage an unverified binary)", wintunVersion, got, wantSHA256)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("open wintun-%s.zip: %w", wintunVersion, err)
	}

	if err := extractOne(zr, "wintun/LICENSE.txt", filepath.Join(outDir, "LICENSE-wintun.txt")); err != nil {
		return err
	}

	for _, arch := range archs {
		wintunArch, ok := wintunArchs[arch]
		if !ok {
			known := make([]string, 0, len(wintunArchs))
			for k := range wintunArchs {
				known = append(known, k)
			}
			sort.Strings(known)
			return fmt.Errorf("no known Wintun architecture mapping for GOARCH %q (known: %v)", arch, known)
		}
		src := fmt.Sprintf("wintun/bin/%s/wintun.dll", wintunArch)
		dst := filepath.Join(outDir, arch, "wintun.dll")
		if err := extractOne(zr, src, dst); err != nil {
			return err
		}
	}
	return nil
}

func download() ([]byte, error) {
	url := wintunBaseURL + "wintun-" + wintunVersion + ".zip"
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url) //nolint:gosec // url is built from this file's own pinned constants, never caller/environment input
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body from %s: %w", url, err)
	}
	return data, nil
}

// extractOne copies the single file name out of zr to dst, creating dst's
// parent directory as needed. name and dst are both this tool's own
// hardcoded/flag-derived paths, never zip-member-controlled -- there is no
// path-traversal surface here to guard against (unlike a generic zip
// extractor), since only the two fixed entry names above are ever read.
func extractOne(zr *zip.Reader, name, dst string) error {
	f, err := zr.Open(name)
	if err != nil {
		return fmt.Errorf("find %q in wintun-%s.zip: %w", name, wintunVersion, err)
	}
	defer func() { _ = f.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // staged release build inputs are conventionally world-readable, no secrets here
		return fmt.Errorf("create directory for %q: %w", dst, err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644) //nolint:gosec // wintun.dll/LICENSE-wintun.txt are redistributed binaries, intentionally world-readable
	if err != nil {
		return fmt.Errorf("create %q: %w", dst, err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, f); err != nil { //nolint:gosec // f is bounded by the zip's own central directory size, and the source archive is hash-verified above
		return fmt.Errorf("write %q: %w", dst, err)
	}
	return nil
}
