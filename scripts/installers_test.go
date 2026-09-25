package scripts

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallPS1InstallsReleaseFromLocalFixture(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell installer smoke test only runs on Windows")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	version := "v9.9.9-test"
	baseUrl := createBaseURLFixture(t, root, tempDir, version, "windows")

	installDir := filepath.Join(tempDir, "install")
	output := runCommand(t, root, []string{
		"pwsh", "-NoProfile", "-File", filepath.Join(root, "scripts", "install.ps1"),
	}, map[string]string{
		"BOXY_BASE_URL":    baseUrl,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     version,
	})

	destination := filepath.Join(installDir, "boxy.exe")
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("expected installed binary at %s: %v", destination, err)
	}

	versionOutput := strings.TrimSpace(runCommand(t, root, []string{destination, "--version"}, nil))
	if versionOutput != "boxy "+version {
		t.Fatalf("unexpected installed version output: %q", versionOutput)
	}

	if !strings.Contains(output, "Done!") {
		t.Fatalf("expected install output to include completion banner, got:\n%s", output)
	}
}

// TestInstallPS1CopiesWintunDLLWhenPresent covers #379 blocker 6's install
// half: createBaseURLFixture's windows zips now always include a wintun.dll
// entry (see buildBoxyBinaryForTarget), matching every real windows archive
// built by .goreleaser.yml from this change onward, so install.ps1 must
// actually copy it into $InstallDir alongside boxy.exe -- packaging it into
// the release archive is necessary but not sufficient if the installer
// silently drops it, which is exactly what it did before this fix (it only
// ever copied boxy.exe out of the extracted archive).
func TestInstallPS1CopiesWintunDLLWhenPresent(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell installer smoke test only runs on Windows")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	version := "v9.9.9-test"
	baseUrl := createBaseURLFixture(t, root, tempDir, version, "windows")

	installDir := filepath.Join(tempDir, "install")
	_ = runCommand(t, root, []string{
		"pwsh", "-NoProfile", "-File", filepath.Join(root, "scripts", "install.ps1"),
	}, map[string]string{
		"BOXY_BASE_URL":    baseUrl,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     version,
	})

	wintunDest := filepath.Join(installDir, "wintun.dll")
	content, err := os.ReadFile(wintunDest)
	if err != nil {
		t.Fatalf("expected wintun.dll installed at %s: %v", wintunDest, err)
	}
	if string(content) != "fixture-wintun-dll-bytes" {
		t.Fatalf("installed wintun.dll content = %q, want the fixture's bytes", content)
	}
}

// TestInstallPS1SkipsWintunDLLWhenArchiveHasNone covers backward
// compatibility with a release built before #379 blocker 6: an archive
// with no wintun.dll entry at all (every release up to and including
// v0.1.68) must still install boxy.exe successfully, with install.ps1's
// Test-Path guard silently skipping the copy rather than erroring.
func TestInstallPS1SkipsWintunDLLWhenArchiveHasNone(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell installer smoke test only runs on Windows")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	version := "v9.9.9-test"

	releaseDir := filepath.Join(tempDir, "release-assets")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", releaseDir, err)
	}
	// Both arches, like releaseAssetsForOS builds for every other fixture:
	// install.ps1 requests whichever one matches the host actually running
	// this test (amd64 or arm64), so a single hardcoded arch would only
	// pass on a matching CI runner.
	var checksumLines []string
	for _, arch := range []string{"amd64", "arm64"} {
		assetName := fmt.Sprintf("boxy_%s_windows_%s.zip", strings.TrimPrefix(version, "v"), arch)
		assetPath := filepath.Join(releaseDir, assetName)
		binaryPath := buildBoxyBinary(t, root, version, "windows", arch)
		// No `extra` entries here, deliberately -- this is the pre-#379
		// archive shape.
		writeZipArchive(t, assetPath, binaryPath, "boxy.exe", nil)
		h := sha256.Sum256(mustReadFile(t, assetPath))
		checksumLines = append(checksumLines, fmt.Sprintf("%x  %s", h, assetName))
	}
	checksums := []byte(strings.Join(checksumLines, "\n") + "\n")

	tagJSON := fmt.Sprintf(`[{"tag_name":%q}]`, version)
	downloadPrefix := "/Geogboe/boxy/releases/download/" + version + "/"
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/Geogboe/boxy/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tagJSON))
	})
	mux.HandleFunc(downloadPrefix, func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, downloadPrefix)
		if name == "checksums.txt" {
			_, _ = w.Write(checksums)
			return
		}
		data, err := os.ReadFile(filepath.Join(releaseDir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	installDir := filepath.Join(tempDir, "install")
	output := runCommand(t, root, []string{
		"pwsh", "-NoProfile", "-File", filepath.Join(root, "scripts", "install.ps1"),
	}, map[string]string{
		"BOXY_BASE_URL":    srv.URL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     version,
	})

	if _, err := os.Stat(filepath.Join(installDir, "boxy.exe")); err != nil {
		t.Fatalf("expected boxy.exe still installed from a pre-wintun archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "wintun.dll")); !os.IsNotExist(err) {
		t.Fatalf("expected no wintun.dll to be installed from an archive that never had one, stat err = %v", err)
	}
	if !strings.Contains(output, "Done!") {
		t.Fatalf("expected install output to include completion banner, got:\n%s", output)
	}
}

func TestInstallPS1UpgradesExistingInstallByDefault(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell installer smoke test only runs on Windows")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	initialVersion := "v9.9.9-test"
	upgradedVersion := "v9.9.10-test"
	initialBaseURL := createBaseURLFixture(t, root, tempDir, initialVersion, "windows")
	upgradedBaseURL := createBaseURLFixture(t, root, tempDir, upgradedVersion, "windows")

	installDir := filepath.Join(tempDir, "install")
	_ = runCommand(t, root, []string{
		"pwsh", "-NoProfile", "-File", filepath.Join(root, "scripts", "install.ps1"),
	}, map[string]string{
		"BOXY_BASE_URL":    initialBaseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     initialVersion,
	})

	output := runCommand(t, root, []string{
		"pwsh", "-NoProfile", "-File", filepath.Join(root, "scripts", "install.ps1"),
	}, map[string]string{
		"BOXY_BASE_URL":    upgradedBaseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     upgradedVersion,
	})

	destination := filepath.Join(installDir, "boxy.exe")
	versionOutput := strings.TrimSpace(runCommand(t, root, []string{destination, "--version"}, nil))
	if versionOutput != "boxy "+upgradedVersion {
		t.Fatalf("expected upgraded version output %q, got %q", "boxy "+upgradedVersion, versionOutput)
	}

	if !strings.Contains(output, "installed to") {
		t.Fatalf("expected upgrade output to contain 'installed to', got:\n%s", output)
	}
}

func TestInstallPS1SkipsUpgradeWhenRequested(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell installer smoke test only runs on Windows")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	initialVersion := "v9.9.9-test"
	upgradedVersion := "v9.9.10-test"
	initialBaseURL := createBaseURLFixture(t, root, tempDir, initialVersion, "windows")
	upgradedBaseURL := createBaseURLFixture(t, root, tempDir, upgradedVersion, "windows")

	installDir := filepath.Join(tempDir, "install")
	_ = runCommand(t, root, []string{
		"pwsh", "-NoProfile", "-File", filepath.Join(root, "scripts", "install.ps1"),
	}, map[string]string{
		"BOXY_BASE_URL":    initialBaseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     initialVersion,
	})

	output := runCommand(t, root, []string{
		"pwsh", "-NoProfile", "-File", filepath.Join(root, "scripts", "install.ps1"),
	}, map[string]string{
		"BOXY_BASE_URL":     upgradedBaseURL,
		"BOXY_INSTALL_DIR":  installDir,
		"BOXY_VERSION":      upgradedVersion,
		"BOXY_SKIP_UPGRADE": "1",
	})

	destination := filepath.Join(installDir, "boxy.exe")
	versionOutput := strings.TrimSpace(runCommand(t, root, []string{destination, "--version"}, nil))
	if versionOutput != "boxy "+initialVersion {
		t.Fatalf("expected installed version to remain %q, got %q", "boxy "+initialVersion, versionOutput)
	}

	if !strings.Contains(output, "skipping upgrade because BOXY_SKIP_UPGRADE=1") {
		t.Fatalf("expected skip-upgrade output, got:\n%s", output)
	}
}

func TestInstallShDeclaresExpectedContracts(t *testing.T) {
	root := repoRoot(t)
	content := string(mustReadFile(t, filepath.Join(root, "scripts", "install.sh")))

	requiredSnippets := []string{
		"Code generated by go generate; DO NOT EDIT",
		"BOXY_VERSION",
		"BOXY_INSTALL_DIR",
		"BOXY_SKIP_UPGRADE",
		"BOXY_FORCE",
		"BOXY_DEBUG",
		"BOXY_BASE_URL",
		"darwin",
		"linux",
		"checksums.txt",
		"releases?per_page=1",
		"${version_num}_${os}_${arch}.tar.gz",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(content, snippet) {
			t.Fatalf("install.sh is missing required snippet %q", snippet)
		}
	}
}

func TestInstallPS1DeclaresExpectedContracts(t *testing.T) {
	root := repoRoot(t)
	content := string(mustReadFile(t, filepath.Join(root, "scripts", "install.ps1")))

	requiredSnippets := []string{
		"Code generated by go generate; DO NOT EDIT",
		"BOXY_VERSION",
		"BOXY_INSTALL_DIR",
		"BOXY_SKIP_UPGRADE",
		"BOXY_FORCE",
		"BOXY_DEBUG",
		"BOXY_BASE_URL",
		"checksums.txt",
		"releases?per_page=1",
		"ARM64",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(content, snippet) {
			t.Fatalf("install.ps1 is missing required snippet %q", snippet)
		}
	}
}

func TestInstallShInstallsReleaseFromLocalFixture(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native shell installer smoke test only runs on linux and darwin")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	version := "v9.9.9-test"
	baseURL := createBaseURLFixture(t, root, tempDir, version, runtime.GOOS)
	installDir := filepath.Join(tempDir, "install")

	output := runCommand(t, root, []string{"bash", filepath.Join(root, "scripts", "install.sh")}, map[string]string{
		"BOXY_BASE_URL":    baseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     version,
	})

	destination := filepath.Join(installDir, "boxy")
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("expected installed binary at %s: %v", destination, err)
	}

	versionOutput := strings.TrimSpace(runCommand(t, root, []string{destination, "--version"}, nil))
	if versionOutput != "boxy "+version {
		t.Fatalf("unexpected installed version output: %q", versionOutput)
	}

	if !strings.Contains(output, "installed to") {
		t.Fatalf("expected install output to contain 'installed to', got:\n%s", output)
	}
}

func TestInstallShUpgradesExistingInstallByDefault(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native shell installer smoke test only runs on linux and darwin")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	initialVersion := "v9.9.9-test"
	upgradedVersion := "v9.9.10-test"
	initialBaseURL := createBaseURLFixture(t, root, tempDir, initialVersion, runtime.GOOS)
	upgradedBaseURL := createBaseURLFixture(t, root, tempDir, upgradedVersion, runtime.GOOS)
	installDir := filepath.Join(tempDir, "install")

	_ = runCommand(t, root, []string{"bash", filepath.Join(root, "scripts", "install.sh")}, map[string]string{
		"BOXY_BASE_URL":    initialBaseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     initialVersion,
	})

	output := runCommand(t, root, []string{"bash", filepath.Join(root, "scripts", "install.sh")}, map[string]string{
		"BOXY_BASE_URL":    upgradedBaseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     upgradedVersion,
	})

	destination := filepath.Join(installDir, "boxy")
	versionOutput := strings.TrimSpace(runCommand(t, root, []string{destination, "--version"}, nil))
	if versionOutput != "boxy "+upgradedVersion {
		t.Fatalf("expected upgraded version output %q, got %q", "boxy "+upgradedVersion, versionOutput)
	}

	if !strings.Contains(output, "installed to") {
		t.Fatalf("expected upgrade output to contain 'installed to', got:\n%s", output)
	}
}

func TestInstallShSkipsUpgradeWhenRequested(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native shell installer smoke test only runs on linux and darwin")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	initialVersion := "v9.9.9-test"
	upgradedVersion := "v9.9.10-test"
	initialBaseURL := createBaseURLFixture(t, root, tempDir, initialVersion, runtime.GOOS)
	upgradedBaseURL := createBaseURLFixture(t, root, tempDir, upgradedVersion, runtime.GOOS)
	installDir := filepath.Join(tempDir, "install")

	_ = runCommand(t, root, []string{"bash", filepath.Join(root, "scripts", "install.sh")}, map[string]string{
		"BOXY_BASE_URL":    initialBaseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     initialVersion,
	})

	output := runCommand(t, root, []string{"bash", filepath.Join(root, "scripts", "install.sh")}, map[string]string{
		"BOXY_BASE_URL":     upgradedBaseURL,
		"BOXY_INSTALL_DIR":  installDir,
		"BOXY_VERSION":      upgradedVersion,
		"BOXY_SKIP_UPGRADE": "1",
	})

	destination := filepath.Join(installDir, "boxy")
	versionOutput := strings.TrimSpace(runCommand(t, root, []string{destination, "--version"}, nil))
	if versionOutput != "boxy "+initialVersion {
		t.Fatalf("expected installed version to remain %q, got %q", "boxy "+initialVersion, versionOutput)
	}

	if !strings.Contains(output, "skipping upgrade because BOXY_SKIP_UPGRADE=1") {
		t.Fatalf("expected skip-upgrade output, got:\n%s", output)
	}
}

func TestInstallShWorksWithPosixSh(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX sh installer smoke test only runs on linux and darwin")
	}

	// Find a POSIX sh that is distinct from bash, preferring dash.
	sh, err := exec.LookPath("dash")
	if err != nil {
		sh = "/bin/sh"
	}

	// Only run if sh does not support pipefail (i.e. it is a strict POSIX sh).
	// If sh is bash in disguise this test is redundant but still harmless.
	if out, _ := exec.Command(sh, "-c", "set -o pipefail").CombinedOutput(); len(out) == 0 {
		t.Skip("sh supports pipefail — not a strict POSIX sh, skipping")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	version := "v9.9.9-test"
	baseURL := createBaseURLFixture(t, root, tempDir, version, runtime.GOOS)
	installDir := filepath.Join(tempDir, "install")

	output := runCommand(t, root, []string{sh, filepath.Join(root, "scripts", "install.sh")}, map[string]string{
		"BOXY_BASE_URL":    baseURL,
		"BOXY_INSTALL_DIR": installDir,
		"BOXY_VERSION":     version,
	})

	destination := filepath.Join(installDir, "boxy")
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("expected installed binary at %s: %v", destination, err)
	}

	if !strings.Contains(output, "installed to") {
		t.Fatalf("expected install output to contain 'installed to', got:\n%s", output)
	}
}

func TestInstallShWSL(t *testing.T) {
	skipInstallerRunInShortMode(t)
	if runtime.GOOS != "windows" {
		t.Skip("WSL installer smoke test only runs on Windows")
	}
	if os.Getenv("BOXY_TEST_WSL") != "1" {
		t.Skip("set BOXY_TEST_WSL=1 to run the WSL installer smoke test")
	}
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		t.Skip("wsl.exe not found")
	}

	root := repoRoot(t)
	tempDir := t.TempDir()
	version := "v9.9.9-test"
	baseURL := createBaseURLFixture(t, root, tempDir, version, "linux")
	installDir := filepath.Join(tempDir, "wsl-install")
	repoRootWSL := toWSLPath(t, root)
	installDirWSL := toWSLPath(t, installDir)

	command := strings.Join([]string{
		"set -euo pipefail",
		"cd " + shellQuote(repoRootWSL),
		"BOXY_BASE_URL=" + shellQuote(baseURL) +
			" BOXY_INSTALL_DIR=" + shellQuote(installDirWSL) +
			" BOXY_VERSION=" + shellQuote(version) +
			" bash ./scripts/install.sh",
		shellQuote(installDirWSL+"/boxy") + " --version",
	}, "; ")

	output := runCommand(t, root, []string{"wsl.exe", "bash", "-lc", command}, nil)
	if !strings.Contains(output, "installed to") {
		t.Fatalf("expected install output to contain 'installed to', got:\n%s", output)
	}
	if !strings.Contains(output, "boxy "+version) {
		t.Fatalf("expected WSL-installed binary version output, got:\n%s", output)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

func createBaseURLFixture(t *testing.T, root, tempDir, version, targetOS string) string {
	t.Helper()
	releaseDir := filepath.Join(tempDir, "release-assets")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", releaseDir, err)
	}

	assets := releaseAssetsForOS(version, targetOS)
	checksumLines := make([]string, 0, len(assets))
	for _, asset := range assets {
		assetPath := filepath.Join(releaseDir, asset.Name)
		buildBoxyBinaryForTarget(t, root, assetPath, version, targetOS, asset.Arch)
		h := sha256.Sum256(mustReadFile(t, assetPath))
		checksumLines = append(checksumLines, fmt.Sprintf("%x  %s", h, asset.Name))
	}
	checksums := []byte(strings.Join(checksumLines, "\n") + "\n")

	tagJSON := fmt.Sprintf(`[{"tag_name":%q}]`, version)
	downloadPrefix := "/Geogboe/boxy/releases/download/" + version + "/"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/Geogboe/boxy/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tagJSON))
	})
	mux.HandleFunc(downloadPrefix, func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, downloadPrefix)
		if name == "checksums.txt" {
			_, _ = w.Write(checksums)
			return
		}
		data, err := os.ReadFile(filepath.Join(releaseDir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

type releaseAsset struct {
	Name string
	Arch string
}

func releaseAssetsForOS(version, targetOS string) []releaseAsset {
	trimmedVersion := strings.TrimPrefix(version, "v")

	switch targetOS {
	case "windows":
		return []releaseAsset{
			{Name: fmt.Sprintf("boxy_%s_windows_amd64.zip", trimmedVersion), Arch: "amd64"},
			{Name: fmt.Sprintf("boxy_%s_windows_arm64.zip", trimmedVersion), Arch: "arm64"},
		}
	case "linux":
		return []releaseAsset{
			{Name: fmt.Sprintf("boxy_%s_linux_amd64.tar.gz", trimmedVersion), Arch: "amd64"},
			{Name: fmt.Sprintf("boxy_%s_linux_arm64.tar.gz", trimmedVersion), Arch: "arm64"},
		}
	case "darwin":
		return []releaseAsset{
			{Name: fmt.Sprintf("boxy_%s_darwin_amd64.tar.gz", trimmedVersion), Arch: "amd64"},
			{Name: fmt.Sprintf("boxy_%s_darwin_arm64.tar.gz", trimmedVersion), Arch: "arm64"},
		}
	default:
		panic("unsupported release fixture OS: " + targetOS)
	}
}

func buildBoxyBinaryForTarget(t *testing.T, root, output, version, goos, goarch string) {
	t.Helper()
	binaryPath := buildBoxyBinary(t, root, version, goos, goarch)
	binaryName := filepath.Base(binaryPath)

	switch filepath.Ext(output) {
	case ".zip":
		// wintun.dll is bundled in every real windows archive from #379
		// blocker 6 onward (see .goreleaser.yml's windows archive) -- matching
		// that here means the many existing windows installer tests below
		// exercise install.ps1's copy-if-present path implicitly, on top of
		// TestInstallPS1CopiesWintunDLLWhenPresent's direct assertion.
		// TestInstallPS1SkipsWintunDLLWhenArchiveHasNone builds its own
		// fixture without this entry to cover an older, pre-#379 archive.
		extra := map[string][]byte{}
		if goos == "windows" {
			extra["wintun.dll"] = []byte("fixture-wintun-dll-bytes")
		}
		writeZipArchive(t, output, binaryPath, binaryName, extra)
	case ".gz":
		writeTarGzArchive(t, output, binaryPath, binaryName)
	default:
		t.Fatalf("unsupported output archive: %s", output)
	}
}

// buildBoxyBinary cross-compiles a real boxy binary for goos/goarch, stamped
// with version -- the shared build step behind every installer fixture
// archive, whatever it ultimately gets packaged into.
func buildBoxyBinary(t *testing.T, root, version, goos, goarch string) string {
	t.Helper()
	buildDir := t.TempDir()
	binaryName := "boxy"
	if goos == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(buildDir, binaryName)

	cmd := exec.Command("go", "build", "-trimpath", "-ldflags",
		"-X main.Version="+version+" -X main.GitCommit=test -X main.BuildDate=2026-03-25T00:00:00Z",
		"-o", binaryPath, "./cmd/boxy")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s/%s boxy binary: %v\n%s", goos, goarch, err, string(out))
	}
	return binaryPath
}

// writeZipArchive zips filePath in as archiveName, plus any extra entries
// (name -> content) -- used to fold a fixture wintun.dll alongside boxy.exe
// without a real download, mirroring what a genuine windows release archive
// contains from #379 blocker 6 onward.
func writeZipArchive(t *testing.T, archivePath, filePath, archiveName string, extra map[string][]byte) {
	t.Helper()

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create zip %s: %v", archivePath, err)
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	entry, err := zw.Create(archiveName)
	if err != nil {
		t.Fatalf("create zip entry %s: %v", archiveName, err)
	}

	source, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open %s: %v", filePath, err)
	}
	defer source.Close()

	if _, err := io.Copy(entry, source); err != nil {
		t.Fatalf("write zip entry %s: %v", archiveName, err)
	}

	for name, content := range extra {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip %s: %v", archivePath, err)
	}
}

func writeTarGzArchive(t *testing.T, archivePath, filePath, archiveName string) {
	t.Helper()

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create tar.gz %s: %v", archivePath, err)
	}
	defer file.Close()

	gzw := gzip.NewWriter(file)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat %s: %v", filePath, err)
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		t.Fatalf("header for %s: %v", filePath, err)
	}
	header.Name = archiveName
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write tar header %s: %v", archiveName, err)
	}

	source, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open %s: %v", filePath, err)
	}
	defer source.Close()

	if _, err := io.Copy(tw, source); err != nil {
		t.Fatalf("write tar body %s: %v", archiveName, err)
	}
}

func toWSLPath(t *testing.T, path string) string {
	t.Helper()
	cleaned := filepath.Clean(path)
	if len(cleaned) < 3 || cleaned[1] != ':' {
		t.Fatalf("unsupported Windows path for WSL conversion: %s", path)
	}

	drive := strings.ToLower(cleaned[:1])
	rest := strings.ReplaceAll(cleaned[2:], `\`, `/`)
	return "/mnt/" + drive + rest
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func runCommand(t *testing.T, dir string, argv []string, extraEnv map[string]string) string {
	t.Helper()
	out, err := execCommand(t, dir, argv, extraEnv)
	if err != nil {
		t.Fatalf("run %q: %v\n%s", strings.Join(argv, " "), err, string(out))
	}
	return string(out)
}

func execCommand(t *testing.T, dir string, argv []string, extraEnv map[string]string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd.CombinedOutput()
}

// skipInstallerRunInShortMode skips a test that actually runs an installer.
// These take minutes (most of CI's Test job time), and the Installer Smoke
// job runs this package without -short, so the -short race matrix doesn't
// need to run them a second time.
func skipInstallerRunInShortMode(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs an installer end to end; covered by the Installer Smoke job (go test ./scripts without -short)")
	}
}
