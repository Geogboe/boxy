// Package buildcfg defines project-level constants used by the install
// script generator and the self-update command.  Changing a value here
// and re-running `go generate ./scripts/...` propagates the change to
// every generated artifact.
package buildcfg

// Repo is the GitHub owner/name for the project.
const Repo = "Geogboe/boxy"

// BinaryName is the name of the compiled executable.
const BinaryName = "boxy"

// DefaultInstallDir is the install directory relative to $HOME.
const DefaultInstallDir = ".local/bin"

// APIBase is the GitHub API root URL.
const APIBase = "https://api.github.com"

// DownloadBase is the GitHub download root URL (release assets).
const DownloadBase = "https://github.com"

// AssetName returns the archive base name for a given version, OS, and
// architecture, matching the GoReleaser name_template.
func AssetName(version, goos, goarch string) string {
	if goos == "windows" && goarch == "arm64" {
		goarch = "amd64"
	}
	return BinaryName + "_" + version + "_" + goos + "_" + goarch
}
