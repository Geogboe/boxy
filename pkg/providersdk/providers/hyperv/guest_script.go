package hyperv

import (
	"context"
	"strings"

	"github.com/Geogboe/boxy/pkg/vmsdk"
)

// The source contains parameter names only; credentials arrive as PSRP arguments.
//
//nolint:gosec // no credential value is embedded in this script
const rotateCredentialScript = `param($username, $password)
$secure = ConvertTo-SecureString $password -AsPlainText -Force
Set-LocalUser -Name $username -Password $secure -ErrorAction Stop`

const assignIPScript = `param($ip, $prefix, $gateway, $dns)
$ErrorActionPreference = 'Stop'
$adapter = Get-NetAdapter | Where-Object { $_.Status -ne 'Disabled' } | Sort-Object InterfaceIndex | Select-Object -First 1
if ($null -eq $adapter) { throw 'no network adapter found in guest' }
$existing = Get-NetIPAddress -InterfaceIndex $adapter.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue
if ($existing) { $existing | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue | Out-Null }
$existingRoute = Get-NetRoute -InterfaceIndex $adapter.InterfaceIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue
if ($existingRoute) { $existingRoute | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue | Out-Null }
$parameters = @{ InterfaceIndex = $adapter.InterfaceIndex; IPAddress = $ip; PrefixLength = [int]$prefix }
if ($gateway) { $parameters.DefaultGateway = $gateway }
New-NetIPAddress @parameters | Out-Null
$servers = @($dns.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ })
if ($servers.Count) { Set-DnsClientServerAddress -InterfaceIndex $adapter.InterfaceIndex -ServerAddresses $servers | Out-Null }
$applied = Get-NetIPAddress -InterfaceIndex $adapter.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -eq $ip -and $_.AddressState -in @('Preferred', 'Tentative') }
if ($null -eq $applied) { throw 'address did not apply in guest' }
if ($gateway) {
    $route = Get-NetRoute -InterfaceIndex $adapter.InterfaceIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue
    if ($null -eq $route) { throw 'default gateway did not apply in guest' }
}`

func rotateGuestCredential(ctx context.Context, exec vmsdk.GuestExec, guestOS, username, password string) (*vmsdk.ExecResult, error) {
	if scriptExec, ok := exec.(vmsdk.GuestExecScript); ok && !strings.EqualFold(guestOS, "linux") {
		return scriptExec.ExecScript(ctx, rotateCredentialScript, username, password)
	}
	cmd, args := rotationCommand(guestOS, username, password)
	return exec.Exec(ctx, cmd, args...)
}
