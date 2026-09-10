package hyperv

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/Geogboe/boxy/pkg/diskjson"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// segmentAllocation is one sandbox's assigned switch/NAT identity, persisted
// so restarts don't lose track of which CIDRs are in use.
type segmentAllocation struct {
	SwitchName string `json:"switch_name"`
	CIDR       string `json:"cidr"`
	Gateway    string `json:"gateway"`
}

type segmentLedgerState struct {
	// NextIndex is the next /29 block offset (0-based) to hand out from
	// segmentBaseCIDR. Only ever increases while entries exist; a released
	// CIDR is tracked in FreedIndexes and reused before NextIndex advances,
	// so long-running hosts don't walk the whole range needlessly.
	NextIndex    int                          `json:"next_index"`
	FreedIndexes []int                        `json:"freed_indexes,omitempty"`
	BySandboxID  map[string]segmentAllocation `json:"by_sandbox_id"`
}

// segmentBaseCIDR is the private range this driver carves per-sandbox /29
// blocks (8 addresses: network, gateway, up to 5 usable hosts, broadcast --
// enough for a small sandbox lab) out of. Chosen from RFC 1918 space
// unlikely to collide with an operator's own LAN (10.250.0.0/16 is well
// outside common home/office 10.0.0.0/8 allocations that start near
// 10.0.x.x or 10.1.x.x).
const segmentBaseCIDR = "10.250.0.0/16"

type segmentLedger struct {
	store *diskjson.Store[segmentLedgerState]
}

func newSegmentLedger(path string) *segmentLedger {
	return &segmentLedger{
		store: diskjson.New(path, func() segmentLedgerState {
			return segmentLedgerState{BySandboxID: make(map[string]segmentAllocation)}
		}),
	}
}

// allocate returns the sandbox's existing CIDR/gateway if one is already
// recorded (idempotent -- a retried CreateSegment call must not hand out a
// second block), or carves and persists a new /29 otherwise.
func (l *segmentLedger) allocate(sandboxID string) (segmentAllocation, error) {
	state, err := l.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		if s.BySandboxID == nil {
			s.BySandboxID = make(map[string]segmentAllocation)
		}
		if _, ok := s.BySandboxID[sandboxID]; ok {
			return s, nil
		}
		index := s.NextIndex
		if n := len(s.FreedIndexes); n > 0 {
			index = s.FreedIndexes[n-1]
			s.FreedIndexes = s.FreedIndexes[:n-1]
		} else {
			s.NextIndex++
		}
		cidr, gateway, err := blockForIndex(index)
		if err != nil {
			return s, err
		}
		s.BySandboxID[sandboxID] = segmentAllocation{
			SwitchName: switchNameForSandbox(sandboxID),
			CIDR:       cidr,
			Gateway:    gateway,
		}
		return s, nil
	})
	if err != nil {
		return segmentAllocation{}, err
	}
	return state.BySandboxID[sandboxID], nil
}

func (l *segmentLedger) release(sandboxID string) error {
	_, err := l.store.Update(func(s segmentLedgerState) (segmentLedgerState, error) {
		if s.BySandboxID == nil {
			return s, nil
		}
		alloc, ok := s.BySandboxID[sandboxID]
		if !ok {
			return s, nil
		}
		// Recover the index from the CIDR to free it for reuse: the block
		// size is fixed (/29 = 8 addresses), so the offset from
		// segmentBaseCIDR's base address, divided by 8, is the index.
		idx, err := indexForBlock(alloc.CIDR)
		if err == nil {
			s.FreedIndexes = append(s.FreedIndexes, idx)
		}
		delete(s.BySandboxID, sandboxID)
		return s, nil
	})
	return err
}

// blockForIndex computes the index-th /29 block within segmentBaseCIDR,
// returning its network CIDR and the first usable address (used as the
// switch's gateway/host-side IP).
func blockForIndex(index int) (cidr string, gateway string, err error) {
	_, base, parseErr := net.ParseCIDR(segmentBaseCIDR)
	if parseErr != nil {
		return "", "", fmt.Errorf("parse segmentBaseCIDR: %w", parseErr)
	}
	ip4 := base.IP.To4()
	if ip4 == nil {
		return "", "", fmt.Errorf("segmentBaseCIDR %q is not IPv4", segmentBaseCIDR)
	}
	offset := uint32(index) * 8
	baseInt := uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
	blockStart := baseInt + offset
	blockIP := net.IPv4(byte(blockStart>>24), byte(blockStart>>16), byte(blockStart>>8), byte(blockStart))
	gatewayInt := blockStart + 1
	gatewayIP := net.IPv4(byte(gatewayInt>>24), byte(gatewayInt>>16), byte(gatewayInt>>8), byte(gatewayInt))
	return fmt.Sprintf("%s/29", blockIP.String()), gatewayIP.String(), nil
}

func indexForBlock(cidr string) (int, error) {
	_, block, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, err
	}
	_, base, err := net.ParseCIDR(segmentBaseCIDR)
	if err != nil {
		return 0, err
	}
	blockIP4, baseIP4 := block.IP.To4(), base.IP.To4()
	if blockIP4 == nil || baseIP4 == nil {
		return 0, fmt.Errorf("non-IPv4 CIDR")
	}
	blockInt := uint32(blockIP4[0])<<24 | uint32(blockIP4[1])<<16 | uint32(blockIP4[2])<<8 | uint32(blockIP4[3])
	baseInt := uint32(baseIP4[0])<<24 | uint32(baseIP4[1])<<16 | uint32(baseIP4[2])<<8 | uint32(baseIP4[3])
	return int((blockInt - baseInt) / 8), nil
}

func switchNameForSandbox(sandboxID string) string {
	return "boxy-sb-" + strings.ReplaceAll(sandboxID, " ", "")
}

// segmentLedgerPathOrDefault returns d's ledger location, defaulting the
// same way Config.DataDir already does elsewhere in this package.
func (d *Driver) segmentLedgerPathOrDefault() string {
	if d.segmentLedgerPath != "" {
		return d.segmentLedgerPath
	}
	return "network-segments.json"
}

func (d *Driver) segments() *segmentLedger {
	return newSegmentLedger(d.segmentLedgerPathOrDefault())
}

// CreateSegment creates a dedicated Internal vSwitch + NAT for one sandbox.
// Internal, not Private: Private would also cut off the host-provided NAT
// (all internet access), which is the wrong default until egress policy
// exists to restrict it deliberately (see the design spec's Decision 1).
func (d *Driver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	alloc, err := d.segments().allocate(sandboxID)
	if err != nil {
		return "", fmt.Errorf("allocate segment CIDR for sandbox %q: %w", sandboxID, err)
	}
	_, err = d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (-not (Get-VMSwitch -Name '%s' -ErrorAction SilentlyContinue)) {
    New-VMSwitch -SwitchName '%s' -SwitchType Internal | Out-Null
}
$adapter = Get-NetAdapter | Where-Object { $_.Name -like "*%s*" } | Select-Object -First 1
if (-not (Get-NetIPAddress -InterfaceAlias $adapter.Name -IPAddress '%s' -ErrorAction SilentlyContinue)) {
    New-NetIPAddress -IPAddress '%s' -PrefixLength 29 -InterfaceAlias $adapter.Name | Out-Null
}
if (-not (Get-NetNat -Name '%s' -ErrorAction SilentlyContinue)) {
    New-NetNat -Name '%s' -InternalIPInterfaceAddressPrefix '%s' | Out-Null
}
`,
		psq(alloc.SwitchName), psq(alloc.SwitchName), psq(alloc.SwitchName),
		psq(alloc.Gateway), psq(alloc.Gateway),
		psq(alloc.SwitchName), psq(alloc.SwitchName), psq(alloc.CIDR)))
	if err != nil {
		_ = d.segments().release(sandboxID)
		return "", fmt.Errorf("create segment for sandbox %q: %w", sandboxID, err)
	}
	return providersdk.SegmentRef(alloc.SwitchName), nil
}

// AttachToSegment moves an already-created, already-running VM's network
// adapter onto the sandbox's segment. This is the same live-reconnect
// PowerShell Driver.Create already uses to attach a new VM to its
// configured switch (driver.go's Create) -- no VM restart, single fast
// call, matching the "must be fast" constraint.
func (d *Driver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	vmName, err := d.vmNameFromID(ctx, providerResourceID)
	if err != nil {
		return fmt.Errorf("resolve VM name for %q: %w", providerResourceID, err)
	}
	_, err = d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Connect-VMNetworkAdapter -VMName '%s' -SwitchName '%s' | Out-Null`,
		psq(vmName), psq(string(ref))))
	if err != nil {
		return fmt.Errorf("attach %q to segment %q: %w", providerResourceID, ref, err)
	}
	return nil
}

// DestroySegment removes the NAT and switch created by CreateSegment.
// Idempotent: a segment already gone (both Get- calls find nothing) is not
// an error, matching Driver.Delete's contract.
func (d *Driver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	switchName := string(ref)
	_, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (Get-NetNat -Name '%s' -ErrorAction SilentlyContinue) {
    Remove-NetNat -Name '%s' -Confirm:$false | Out-Null
}
if (Get-VMSwitch -Name '%s' -ErrorAction SilentlyContinue) {
    Remove-VMSwitch -Name '%s' -Force | Out-Null
}
`, psq(switchName), psq(switchName), psq(switchName), psq(switchName)))
	if err != nil {
		return fmt.Errorf("destroy segment %q: %w", ref, err)
	}
	// Best-effort: the ledger entry is keyed by sandbox ID, not switch name,
	// and DestroySegment only receives the SegmentRef (switch name). The
	// caller (Plan 1b's allocation-teardown wiring) is responsible for
	// calling ledger release via the sandbox ID it already has; this
	// method's job is the PowerShell teardown only. (No action needed here
	// -- documented so Plan 1b's author doesn't have to rediscover this.)
	return nil
}
