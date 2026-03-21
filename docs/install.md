# Install Boxy

Boxy install scripts download a published GitHub release binary, verify it against the release `checksums.txt`, and install it into a user-local bin directory.

## Supported Targets

- Windows PowerShell: `windows/amd64`
- Linux: `linux/amd64`, `linux/arm64`

## Defaults

- Windows install dir: `%LOCALAPPDATA%\Programs\boxy\bin`
- Linux install dir: `$HOME/.local/bin`
- `latest` means the newest published GitHub release, including prereleases

## Windows

Run from PowerShell:

```powershell
& ([scriptblock]::Create((Invoke-RestMethod 'https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.ps1')))
```

Install a specific release:

```powershell
& ([scriptblock]::Create((Invoke-RestMethod 'https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.ps1'))) -Version v0.1.5
```

Important behavior:

- Verifies the downloaded `.exe` against `checksums.txt`
- Updates the user `Path` only when using the default install directory
- Refuses to overwrite an existing binary unless `-Force` is provided
- Supports `-Verbose` for install diagnostics

## Linux

Run from Bash:

```bash
curl -fsSL https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.sh | bash
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.sh | bash -s -- --version v0.1.5
```

Important behavior:

- Verifies the downloaded binary against `checksums.txt`
- Installs to `~/.local/bin` by default
- Prints the exact `PATH` export snippet if the install directory is not already on `PATH`
- Refuses to overwrite an existing binary unless `--force` is provided
- Supports `--debug` for install diagnostics

## Verify

After install:

```bash
boxy --version
boxy version
```

`boxy --version` prints a short machine-friendly version string. `boxy version` prints version, commit, and build date.
