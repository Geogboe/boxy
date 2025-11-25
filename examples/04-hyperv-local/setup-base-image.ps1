# Setup Base Image for Boxy Hyper-V Provider
#
# This script helps create the necessary directory structure and
# provides guidance for creating a base VHD image.
#
# IMPORTANT: Run this as Administrator

param(
    [string]$BaseDir = "C:\ProgramData\Boxy",
    [string]$ImageName = "windows-server-2022",
    [string]$ISOPath = ""
)

# Check if running as Administrator
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
    Write-Error "This script must be run as Administrator"
    Write-Host "Right-click PowerShell and select 'Run as Administrator'"
    exit 1
}

# Check if Hyper-V is available
Write-Host "Checking Hyper-V availability..." -ForegroundColor Cyan
try {
    $vmms = Get-Service vmms -ErrorAction Stop
    if ($vmms.Status -ne "Running") {
        Write-Error "Hyper-V service (vmms) is not running. Status: $($vmms.Status)"
        Write-Host "Start it with: Start-Service vmms"
        exit 1
    }
    Write-Host "  ✓ Hyper-V service is running" -ForegroundColor Green
} catch {
    Write-Error "Hyper-V is not installed on this system"
    Write-Host ""
    Write-Host "To install Hyper-V, run:"
    Write-Host "  Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All"
    Write-Host ""
    Write-Host "Then reboot your computer."
    exit 1
}

# Create directory structure
Write-Host ""
Write-Host "Creating Boxy directory structure..." -ForegroundColor Cyan

$dirs = @(
    "$BaseDir\BaseImages",
    "$BaseDir\VMs",
    "$BaseDir\VHDs"
)

foreach ($dir in $dirs) {
    if (Test-Path $dir) {
        Write-Host "  ✓ $dir (exists)" -ForegroundColor Gray
    } else {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        Write-Host "  ✓ $dir (created)" -ForegroundColor Green
    }
}

# Check for existing base image
$baseImagePath = "$BaseDir\BaseImages\$ImageName.vhdx"
Write-Host ""
Write-Host "Checking for base image: $baseImagePath" -ForegroundColor Cyan

if (Test-Path $baseImagePath) {
    Write-Host "  ✓ Base image already exists!" -ForegroundColor Green
    $vhd = Get-VHD -Path $baseImagePath
    Write-Host "    Size: $([math]::Round($vhd.Size / 1GB, 2)) GB"
    Write-Host "    File size: $([math]::Round($vhd.FileSize / 1GB, 2)) GB"
    Write-Host ""
    Write-Host "You're ready to use Boxy! Run:" -ForegroundColor Green
    Write-Host "  boxy.exe serve --config examples\04-hyperv-local\boxy.yaml"
    exit 0
}

# Base image doesn't exist - provide guidance
Write-Host "  Base image not found. You need to create it." -ForegroundColor Yellow
Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  Base Image Setup Options" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# Option 1: Copy existing VM
Write-Host "Option 1: Use Existing VM" -ForegroundColor Yellow
Write-Host "─────────────────────────" -ForegroundColor Gray
Write-Host ""
Write-Host "If you have an existing Hyper-V VM, copy its VHD:"
Write-Host ""
Write-Host '  Stop-VM -Name "YourVM"' -ForegroundColor White
Write-Host "  Copy-Item \"C:\Path\To\YourVM.vhdx\" ``" -ForegroundColor White
Write-Host "      -Destination \"$baseImagePath\"" -ForegroundColor White
Write-Host ""

# Option 2: Create new
Write-Host "Option 2: Create New Base Image" -ForegroundColor Yellow
Write-Host "────────────────────────────────" -ForegroundColor Gray
Write-Host ""

if ($ISOPath -and (Test-Path $ISOPath)) {
    Write-Host "ISO found: $ISOPath" -ForegroundColor Green
    Write-Host ""
    Write-Host "Run these commands to create a base image:" -ForegroundColor Cyan
    Write-Host ""

    $setupScript = @"
# 1. Create VHD
New-VHD -Path "$baseImagePath" ``
    -SizeBytes 60GB -Dynamic

# 2. Create temporary VM for setup
New-VM -Name "BaseImage-$ImageName" ``
    -MemoryStartupBytes 4GB ``
    -Generation 2 ``
    -VHDPath "$baseImagePath"

# 3. Add Windows installation ISO
Add-VMDvdDrive -VMName "BaseImage-$ImageName" ``
    -Path "$ISOPath"

# 4. Connect to Default Switch (provides DHCP/internet)
Connect-VMNetworkAdapter -VMName "BaseImage-$ImageName" ``
    -SwitchName "Default Switch"

# 5. Start VM and connect to it
Start-VM -Name "BaseImage-$ImageName"
vmconnect localhost "BaseImage-$ImageName"

# 6. Inside the VM:
#    - Install Windows
#    - Install Windows Updates
#    - Run: Enable-PSRemoting -Force
#    - Shut down Windows

# 7. After Windows shuts down, remove the VM:
Remove-VM -Name "BaseImage-$ImageName" -Force

# Done! Your base image is ready at:
# $baseImagePath
"@

    Write-Host $setupScript -ForegroundColor White

} else {
    Write-Host "Provide the path to a Windows ISO and run again:" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "  .\setup-base-image.ps1 -ISOPath \"C:\ISOs\WindowsServer2022.iso\"" -ForegroundColor White
    Write-Host ""
    Write-Host "Or follow the manual steps in README.md"
}

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""
Write-Host "After creating the base image, you can start Boxy with:" -ForegroundColor Cyan
Write-Host "  boxy.exe serve --config examples\04-hyperv-local\boxy.yaml" -ForegroundColor White
Write-Host ""
