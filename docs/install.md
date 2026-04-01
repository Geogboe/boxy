# Install Boxy

Boxy install scripts download a published GitHub release binary, verify it against the release `checksums.txt`, and install it into a user-local bin directory.

## Supported Targets

- Windows PowerShell: `windows/amd64`
- Windows on ARM64: installs the published `windows/amd64` binary and relies on Windows x64 emulation
- Linux: `linux/amd64`, `linux/arm64`
- macOS: `darwin/amd64`, `darwin/arm64`

## Defaults

- Windows install dir: `$HOME\.local\bin`
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

## Environment Variables

Both installers accept only environment variables — there are no CLI flags.

| Variable | Description |
|----------|-------------|
| `BOXY_VERSION` | Install a specific tag (e.g. `v0.1.9`). Defaults to latest. |
| `BOXY_INSTALL_DIR` | Override the destination directory. |
| `BOXY_FORCE=1` | Overwrite an existing binary. |
| `BOXY_DEBUG=1` | Enable verbose installer output. |

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

- Verifies the downloaded archive against `checksums.txt`
- Prints PATH instructions if the install directory is not in `$env:Path` — does not modify PATH automatically
- Refuses to overwrite an existing binary unless `BOXY_FORCE=1` is set

## Linux / macOS

Run from a POSIX shell:

```bash
curl -fsSL https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.sh | bash
```

Install a specific release:

```bash
BOXY_VERSION=v0.1.5 curl -fsSL https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.sh | bash
```

Important behavior:

- Verifies the downloaded binary against `checksums.txt`
- Installs to `~/.local/bin` by default
- Prints a shell-specific `PATH` remediation command when the install directory is not on `PATH`
- Refuses to overwrite an existing binary unless `BOXY_FORCE=1` is set

## Update

Once installed, boxy can update itself:

```bash
boxy update
```

Check for an available update without installing:

```bash
boxy update --check
```

Install a specific version:

```bash
boxy update --version v0.1.9
```

Environment variables:

| Variable | Description |
|----------|-------------|
| `BOXY_GITHUB_TOKEN` | GitHub API token to avoid rate limits. |

## Verify

After install:

```bash
boxy --version
boxy version
```

`boxy --version` prints a short machine-friendly version string. `boxy version` prints version, commit, and build date.
