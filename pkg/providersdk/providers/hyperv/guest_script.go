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
$adapter = Get-CimInstance -Namespace root/StandardCimv2 -ClassName MSFT_NetAdapter -Filter 'Hidden = FALSE AND (InterfaceOperationalStatus <> 2 OR InterfaceAdminStatus <> 2)' | Sort-Object InterfaceIndex | Select-Object -First 1
if ($null -eq $adapter) { throw 'no network adapter found in guest' }
$nativeGateway = if ($gateway) { $gateway } else { 'none' }
& "$env:SystemRoot/System32/netsh.exe" interface ipv4 set address "name=$($adapter.InterfaceIndex)" source=static "address=$ip/$prefix" "gateway=$nativeGateway" store=persistent | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'static address command failed in guest' }
$servers = @($dns.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ })
foreach ($family in @('ipv4', 'ipv6')) {
    $familyServers = @($servers | Where-Object { if ($family -eq 'ipv6') { $_ -match ':' } else { $_ -notmatch ':' } })
    if (!$familyServers.Count) { continue }
    & "$env:SystemRoot/System32/netsh.exe" interface $family set dnsservers "name=$($adapter.InterfaceIndex)" source=static "address=$($familyServers[0])" validate=no | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'DNS configuration command failed in guest' }
    for ($serverIndex = 1; $serverIndex -lt $familyServers.Count; $serverIndex++) {
        & "$env:SystemRoot/System32/netsh.exe" interface $family add dnsservers "name=$($adapter.InterfaceIndex)" "address=$($familyServers[$serverIndex])" "index=$($serverIndex+1)" validate=no | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'DNS configuration command failed in guest' }
    }
    $addressFamily = if ($family -eq 'ipv6') { 23 } else { 2 }
    $configuredDNS = Get-CimInstance -Namespace root/StandardCimv2 -ClassName MSFT_DNSClientServerAddress -Filter "InterfaceIndex = $($adapter.InterfaceIndex) AND AddressFamily = $addressFamily"
    if (($configuredDNS.ServerAddresses -join ',') -ne ($familyServers -join ',')) { throw 'DNS servers did not apply in guest' }
}
$filter = "InterfaceIndex = $($adapter.InterfaceIndex) AND AddressFamily = 2"
$applied = Get-CimInstance -Namespace root/StandardCimv2 -ClassName MSFT_NetIPAddress -Filter $filter | Where-Object { $_.IPAddress -eq $ip -and $_.PrefixLength -eq [int]$prefix -and $_.AddressState -in @(1,4) }
if ($null -eq $applied) { throw 'address did not apply in guest' }
$routes = @(Get-CimInstance -Namespace root/StandardCimv2 -ClassName MSFT_NetRoute -Filter $filter | Where-Object { $_.DestinationPrefix -eq '0.0.0.0/0' })
if ($gateway) {
    $route = $routes | Where-Object { $_.NextHop -eq $gateway }
    if ($null -eq $route) { throw 'default gateway did not apply in guest' }
} elseif ($routes.Count) {
    throw 'unexpected default gateway remains in guest'
}`

func rotateGuestCredential(ctx context.Context, exec vmsdk.GuestExec, guestOS, username, password string) (*vmsdk.ExecResult, error) {
	if scriptExec, ok := exec.(vmsdk.GuestExecScript); ok && !strings.EqualFold(guestOS, "linux") {
		return scriptExec.ExecScript(ctx, rotateCredentialScript, username, password)
	}
	cmd, args := rotationCommand(guestOS, username, password)
	return exec.Exec(ctx, cmd, args...)
}
