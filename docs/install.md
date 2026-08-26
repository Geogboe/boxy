# Install Boxy

Boxy install scripts download a published GitHub release binary, verify it against the release `checksums.txt`, and install it into a user-local bin directory.

## Supported Targets

- Windows: `windows/amd64`, `windows/arm64`
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
- `checksums.txt.sigstore.json` — cosign keyless signature bundle for `checksums.txt` (see [Verify Release Signature](#verify-release-signature))

## Environment Variables

Both installers accept only environment variables — there are no CLI flags.

| Variable | Description |
|----------|-------------|
| `BOXY_VERSION` | Install a specific tag (e.g. `v0.1.9`). Defaults to latest. |
| `BOXY_INSTALL_DIR` | Override the destination directory. |
| `BOXY_SKIP_UPGRADE=1` | Keep an existing install unchanged instead of upgrading it. |
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
- Upgrades an existing install automatically by default
- Skips replacing an existing install when `BOXY_SKIP_UPGRADE=1` is set

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
- Upgrades an existing install automatically by default
- Skips replacing an existing install when `BOXY_SKIP_UPGRADE=1` is set

## Verify Release Signature

Both installers verify `checksums.txt` automatically, but neither verifies
its signature yet — that's tracked separately in
[#231](https://github.com/Geogboe/boxy/issues/231). Every release since #55
also publishes `checksums.txt.sigstore.json`, a keyless [cosign](https://docs.sigstore.dev/)
signature bundle proving `checksums.txt` was produced by this repo's own
`release.yml` workflow (not just "someone with a key approved it" — the
identity checked below is the workflow itself). To verify manually, with the
[cosign CLI](https://docs.sigstore.dev/cosign/system_config/installation/)
installed:

```bash
curl -fsSLO https://github.com/Geogboe/boxy/releases/download/<version>/checksums.txt
curl -fsSLO https://github.com/Geogboe/boxy/releases/download/<version>/checksums.txt.sigstore.json

cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/Geogboe/boxy/\.github/workflows/release\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

See [docs/adr/0014-release-signing-with-keyless-cosign.md](adr/0014-release-signing-with-keyless-cosign.md)
for the mechanism and why keyless was chosen over a GPG subkey.

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

### Upgrading a server with remote agents

A remote `boxy agent` and the `boxy serve` it connects to must be running
the **exact same** build version. On registration, the server rejects any
agent whose version doesn't match its own (see
[ADR-0005](adr/0005-remote-agent-transport-and-registration.md)) — there is
no compatibility window or rolling-upgrade path. `boxy update` also
restarts any installed `boxy-agent`/`boxy-serve` service after upgrading it
(see [service-install.md](service-install.md)), so updating either side
takes it offline until the other side is updated to match.

When operating remote agents, upgrade the server and every connected agent
together rather than one at a time — for example, update all agents first
(they'll retry registration with backoff until the server matches), then
update the server, or take agents offline for the duration of the upgrade.

## Run as a background service

To have `boxy agent serve` or `boxy serve` start automatically and
survive reboot/logout instead of running in a foreground terminal, see
[Install boxy agent / boxy serve as a background service](service-install.md).

## Verify

After install:

```bash
boxy --version
boxy version
```

`boxy --version` prints a short machine-friendly version string. `boxy version` prints version, commit, and build date.
