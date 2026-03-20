[CmdletBinding()]
param(
    [string]$Version = $(if ($env:BOXY_INSTALL_VERSION) { $env:BOXY_INSTALL_VERSION } else { "latest" }),
    [string]$InstallDir = $(Join-Path $env:LOCALAPPDATA "Programs\boxy\bin"),
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$repo = "Geogboe/boxy"
$defaultInstallDir = Join-Path $env:LOCALAPPDATA "Programs\boxy\bin"

function Write-DebugLog {
    param([string]$Message)
    Write-Verbose $Message
}

function Fail {
    param([string]$Message)
    throw $Message
}

function Get-ProcessorArchitecture {
    if ($env:PROCESSOR_ARCHITEW6432) {
        return $env:PROCESSOR_ARCHITEW6432
    }
    return $env:PROCESSOR_ARCHITECTURE
}

function Resolve-LatestTag {
    param([string]$Repo)

    Write-DebugLog "Resolving latest release tag"
    $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=1"
    if (-not $releases) {
        Fail "Could not resolve latest release tag."
    }

    if ($releases -is [System.Array]) {
        return $releases[0].tag_name
    }
    return $releases.tag_name
}

function Ensure-UserPathContains {
    param([string]$PathEntry)

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($userPath) {
        $parts = $userPath.Split(';', [System.StringSplitOptions]::RemoveEmptyEntries)
    }

    if ($parts -contains $PathEntry) {
        return $false
    }

    $newPath = if ($userPath) { "$userPath;$PathEntry" } else { $PathEntry }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    if (-not (($env:Path -split ';') -contains $PathEntry)) {
        $env:Path = "$env:Path;$PathEntry"
    }
    return $true
}

$arch = Get-ProcessorArchitecture
if ($arch -notin @("AMD64", "x86_64")) {
    Fail "Unsupported architecture: $arch. Windows release assets are currently available for amd64 only."
}

if ($Version -eq "latest") {
    $Version = Resolve-LatestTag -Repo $repo
}

$asset = "boxy-windows-amd64.exe"
$baseUrl = "https://github.com/$repo/releases/download/$Version"
$downloadUrl = "$baseUrl/$asset"
$checksumsUrl = "$baseUrl/checksums.txt"

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("boxy-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir | Out-Null

try {
    Write-DebugLog "Version: $Version"
    Write-DebugLog "InstallDir: $InstallDir"
    Write-DebugLog "Asset: $asset"

    $downloadedAsset = Join-Path $tempDir $asset
    $checksumsPath = Join-Path $tempDir "checksums.txt"

    Invoke-WebRequest -Uri $downloadUrl -OutFile $downloadedAsset
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsPath

    $checksumLine = Select-String -Path $checksumsPath -Pattern ([regex]::Escape($asset)) | Select-Object -First 1
    if (-not $checksumLine) {
        Fail "Checksum for $asset not found."
    }

    $expectedChecksum = ($checksumLine.Line -split '\s+')[0].Trim()
    $actualChecksum = (Get-FileHash -Path $downloadedAsset -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expectedChecksum.ToLowerInvariant() -ne $actualChecksum) {
        Fail "Checksum mismatch for $asset."
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $destination = Join-Path $InstallDir "boxy.exe"
    if ((Test-Path $destination) -and -not $Force) {
        Fail "$destination already exists. Re-run with -Force to overwrite."
    }

    Copy-Item -Path $downloadedAsset -Destination $destination -Force

    $versionOutput = & $destination --version
    Write-Host "Installed $versionOutput to $destination"

    if ($InstallDir -eq $defaultInstallDir) {
        if (Ensure-UserPathContains -PathEntry $InstallDir) {
            Write-Host "Added $InstallDir to your user PATH. Restart your shell to pick it up everywhere."
        }
    }
    elseif (-not (($env:Path -split ';') -contains $InstallDir)) {
        Write-Host "$InstallDir is not on PATH. Add it to PATH to run 'boxy' without the full path."
    }
}
finally {
    if (Test-Path $tempDir) {
        Remove-Item -Recurse -Force -Path $tempDir
    }
}
