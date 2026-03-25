# Install Boxy

Boxy install scripts download a published GitHub release binary, verify it against the release `checksums.txt`, and install it into a user-local bin directory.

## Supported Targets

- Windows PowerShell: `windows/amd64`
- Windows on ARM64: installs the published `windows/amd64` binary and relies on Windows x64 emulation
- Linux: `linux/amd64`, `linux/arm64`
- macOS: `darwin/amd64`, `darwin/arm64`

## Defaults

- Windows install dir: `%LOCALAPPDATA%\Programs\boxy\bin`
- Linux/macOS install dir: `$HOME/.local/bin`
- `latest` means the newest published GitHub release, including prereleases

## Published Assets

The installers target the GoReleaser archives published by [release.yml](../.github/workflows/release.yml):

- `boxy_<version>_linux_amd64.tar.gz`
- `boxy_<version>_linux_arm64.tar.gz`
- `boxy_<version>_darwin_amd64.tar.gz`
- `boxy_<version>_darwin_arm64.tar.gz`
- `boxy_<version>_windows_amd64.zip`
- `checksums.txt`

## Environment Overrides

Preferred Boxy-specific variables:

- `BOXY_VERSION`: install a specific tag instead of `latest`
- `BOXY_INSTALL_DIR`: override the destination directory
- `BOXY_FORCE=1`: overwrite an existing install
- `BOXY_DEBUG=1`: enable installer debug logging

Compatibility aliases are also supported:

- `BOXY_INSTALL_VERSION`
- `INSTALLER_VERSION`
- `INSTALLER_INSTALL_DIR`
- `INSTALLER_FORCE`
- `INSTALLER_DEBUG`

## Windows

Run from PowerShell:

```powershell
irm https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.ps1 | iex
```

Install a specific release:

```powershell
$env:BOXY_VERSION = 'v0.1.5'
irm https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.ps1 | iex
```

Important behavior:

- Verifies the downloaded `.exe` against `checksums.txt`
- Adds the default install directory to the user `Path` when needed
- Prints an explicit remediation command instead of editing `Path` automatically for custom install directories
- Refuses to overwrite an existing binary unless `-Force` is provided
- Supports `-Verbose`, `BOXY_DEBUG=1`, or `INSTALLER_DEBUG=1` for install diagnostics

## Linux / macOS

Run from a POSIX shell:

```bash
curl -fsSL https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.sh | sh
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.sh | BOXY_VERSION=v0.1.5 sh
```

Important behavior:

- Verifies the downloaded binary against `checksums.txt`
- Installs to `~/.local/bin` by default
- Prints an exact shell-specific PATH remediation command when the install directory is not already on `PATH`
- Refuses to overwrite an existing binary unless `--force` is provided
- Supports `--debug`, `BOXY_DEBUG=1`, or `INSTALLER_DEBUG=1` for install diagnostics

## Verify

After install:

```bash
boxy --version
boxy version
```

`boxy --version` prints a short machine-friendly version string. `boxy version` prints version, commit, and build date.
