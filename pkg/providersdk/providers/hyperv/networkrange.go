package hyperv

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// networkRangeFieldSep delimits NetworkRanges' PowerShell output: one line
// per discovered IPv4 address on the switch's host vNIC, "|"-separated
// fields (address, prefix length, comma-joined Get-NetNat internal
// prefixes).
const networkRangeFieldSep = "|"

// NetworkRanges implements providersdk.NetworkRangeReporter. It discovers
// switchName's real IPv4 range(s) from the host's own vEthernet adapter for
// that switch (Get-NetIPAddress) — not from Get-VMSwitch itself, which has
// no IP address of its own. Hyper-V creates a "vEthernet (<switch name>)"
// host network adapter for an Internal or Default switch, carrying the
// address that is that range's gateway; an External switch's host adapter
// instead carries the physical NIC's own LAN address, which callers should
// not treat as a per-VM range even though it's still reported here (see
// validateNetworkRange's containment check, which handles that the same way
// as any other non-matching discovered range).
//
// Get-NetNat has no field naming which switch it backs, so its
// InternalIPInterfaceAddressPrefix values are cross-checked in Go against
// each discovered address's own network prefix instead — best-effort: a
// mismatch or Get-NetNat failure only affects NetworkRange.NATBacked, never
// CIDR/Gateway. See ADR-0013.
//
// A switch with no discoverable IPv4 address (e.g. a Private switch, which
// has no host vNIC at all) is not an error: it returns (nil, nil). A
// non-nil error means the query itself could not be completed.
func (d *Driver) NetworkRanges(ctx context.Context, switchName string) ([]providersdk.NetworkRange, error) {
	switchName = strings.TrimSpace(switchName)
	if switchName == "" {
		return nil, fmt.Errorf("switch name is required")
	}

	alias := fmt.Sprintf("vEthernet (%s)", switchName)
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$addrs = Get-NetIPAddress -InterfaceAlias '%s' -AddressFamily IPv4 -ErrorAction SilentlyContinue
if ($null -eq $addrs) { return }
$nats = @()
try { $nats = @(Get-NetNat -ErrorAction Stop) } catch { $nats = @() }
$natPrefixes = ($nats | ForEach-Object { $_.InternalIPInterfaceAddressPrefix }) -join ','
foreach ($a in @($addrs)) {
  "$($a.IPAddress)%s$($a.PrefixLength)%s$natPrefixes"
}
`, psq(alias), networkRangeFieldSep, networkRangeFieldSep)

	out, err := d.ps(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("hyperv discover network range for switch %q: %w", switchName, err)
	}
	return parseNetworkRanges(out), nil
}

// parseNetworkRanges parses NetworkRanges' PowerShell output. Malformed
// lines/fields are skipped rather than failing the whole call — the query
// itself already succeeded, so a partially-parseable result is still more
// useful to a caller than none at all.
//
// The comma-joined NAT prefix list is normally identical on every line (the
// PowerShell script computes it once, before its per-address loop), so it's
// memoized by raw field text rather than re-split-and-reparsed on every
// address line — real output for a switch with more than one bound IPv4
// address doesn't redo identical work. The memo key is the field's own text,
// not "first line wins": each line's field is still honored on its own
// terms, so nothing here depends on every line actually agreeing.
func parseNetworkRanges(out string) []providersdk.NetworkRange {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}

	var lastNATField string
	var natPrefixes []netip.Prefix
	haveNATPrefixes := false

	var ranges []providersdk.NetworkRange
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, networkRangeFieldSep, 3)
		if len(parts) < 2 {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(parts[0]))
		if err != nil || !addr.Is4() {
			continue
		}
		prefixLen, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		prefix, err := addr.Prefix(prefixLen)
		if err != nil {
			continue
		}

		natField := ""
		if len(parts) == 3 {
			natField = parts[2]
		}
		if !haveNATPrefixes || natField != lastNATField {
			natPrefixes = parseNATPrefixes(natField)
			lastNATField = natField
			haveNATPrefixes = true
		}

		natBacked := false
		for _, natPrefix := range natPrefixes {
			if natPrefix.Masked() == prefix.Masked() {
				natBacked = true
				break
			}
		}

		ranges = append(ranges, providersdk.NetworkRange{
			CIDR:      prefix.Masked().String(),
			Gateway:   addr.String(),
			NATBacked: natBacked,
		})
	}
	return ranges
}

// parseNATPrefixes parses a comma-joined list of Get-NetNat
// InternalIPInterfaceAddressPrefix values, skipping any that don't parse as
// a CIDR rather than failing the whole list over one bad entry.
func parseNATPrefixes(field string) []netip.Prefix {
	var prefixes []netip.Prefix
	for natPrefixStr := range strings.SplitSeq(field, ",") {
		natPrefixStr = strings.TrimSpace(natPrefixStr)
		if natPrefixStr == "" {
			continue
		}
		natPrefix, err := netip.ParsePrefix(natPrefixStr)
		if err != nil {
			continue
		}
		prefixes = append(prefixes, natPrefix)
	}
	return prefixes
}

// declaredNetworkPrefix computes the network prefix validateNetworkRange
// checks against a switch's discovered range(s): netCfg.Range verbatim in
// range mode, or netCfg.StaticIP treated as a host (/32) prefix in
// static_ip mode. NetworkConfig.validate rejects setting both, so exactly
// one is ever populated by the time Create reaches this check — the two
// modes get the same drift/typo protection rather than only range mode,
// since a static_ip pool declaring a switch is exactly as exposed to a
// mistyped or drifted address as a range-mode one.
func declaredNetworkPrefix(netCfg *NetworkConfig) (netip.Prefix, string, error) {
	if r := strings.TrimSpace(netCfg.Range); r != "" {
		prefix, err := netip.ParsePrefix(r)
		return prefix, r, err
	}
	if ip := strings.TrimSpace(netCfg.StaticIP); ip != "" {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return netip.Prefix{}, ip, err
		}
		return netip.PrefixFrom(addr, addr.BitLen()), ip, nil
	}
	return netip.Prefix{}, "", fmt.Errorf("neither network.range nor network.static_ip is set")
}

// validateNetworkRange cross-checks netCfg's declared address (network.range
// or network.static_ip) against switchName's real discovered IPv4 range(s)
// before Create commits any host resources (New-VHD/New-VM), catching an
// operator typo or host-config drift that would otherwise either hand out
// an address that doesn't route or collide with an address already in use
// outside Boxy's knowledge (see #223, ADR-0013).
//
// Containment, not equality: the declared address/range only needs to fall
// within a discovered range, since a sensible operator config often carves
// a sub-range (e.g. a /25) out of a switch's wider (e.g. /24) NAT scope to
// avoid colliding with DHCP or other range consumers on the same switch.
//
// Only a positive contradiction — discovery succeeded and none of the
// discovered ranges contain the declared one — is a hard error.
// Discovery being indeterminate (the query failed, or came back empty —
// e.g. a Private switch with no host vNIC) is not: it logs a warning and
// proceeds with the declared value trusted, the same as before this
// capability existed. Making an unverifiable PowerShell query a hard gate
// on all provisioning would be worse than the gap #223 closes.
//
// The discovery query is bounded by d.memQueryTimeout() — reused rather
// than adding a third "bounded live host query" duration field, the same
// call resolveCreatedVMID already makes reusing d.bestEffortInterval() for
// its own retry pacing. Without a bound here, a hung Get-NetIPAddress/
// Get-NetNat on a degraded host would block Create indefinitely on a ctx
// that may have no deadline of its own (e.g. the background reconcile
// loop's), exactly the class of hang checkHostHealth/reserveMemory's own
// timeouts in this file already exist to prevent.
func (d *Driver) validateNetworkRange(ctx context.Context, switchName string, netCfg *NetworkConfig) error {
	declared, declaredLabel, err := declaredNetworkPrefix(netCfg)
	if err != nil {
		// NetworkConfig.validate() already rejects an invalid range/static_ip
		// before Create reaches here, so this should be unreachable — but
		// propagate rather than swallow it defensively: proceeding on an
		// unparseable declared address would only defer the same failure to
		// guest personalization with a worse error, and a config.validate()
		// regression should surface here loudly, not silently pass through.
		return fmt.Errorf("config.network: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, d.memQueryTimeout())
	defer cancel()
	ranges, err := d.NetworkRanges(queryCtx, switchName)
	if err != nil {
		slog.Default().WarnContext(ctx, "hyperv: could not discover switch's real IPv4 range; proceeding with the declared network config unverified",
			"switch", switchName, "declared", declaredLabel, "error", err)
		return nil
	}
	if len(ranges) == 0 {
		slog.Default().WarnContext(ctx, "hyperv: switch has no discoverable IPv4 range (private switch, or query returned nothing); proceeding with the declared network config unverified",
			"switch", switchName, "declared", declaredLabel)
		return nil
	}

	discoveredCIDRs := make([]string, 0, len(ranges))
	for _, r := range ranges {
		discoveredCIDRs = append(discoveredCIDRs, r.CIDR)
		discovered, err := netip.ParsePrefix(r.CIDR)
		if err != nil {
			continue
		}
		if discovered.Bits() <= declared.Bits() && discovered.Contains(declared.Addr()) {
			return nil
		}
	}

	return fmt.Errorf("config.network's declared address %s does not fall within switch %q's discovered IPv4 range(s) [%s] — check for a typo in network.range/network.static_ip or host network drift",
		declaredLabel, switchName, strings.Join(discoveredCIDRs, ", "))
}
