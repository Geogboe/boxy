package hyperv

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/Geogboe/boxy/pkg/diskjson"
)

// ledgerFilename is the JSON file name for the range-based IP allocation
// ledger, written under Config.DataDir. See ADR-0012.
const ledgerFilename = "hyperv-ip-ledger.json"

// ledgerEntry is one resource's range-based IP allocation record. It is a
// self-contained provenance snapshot of the NetworkConfig in effect at
// Create time — not a pointer back to live pool config — so a later edit
// to a pool's declared gateway/DNS doesn't retroactively change what an
// already-provisioned VM was configured with. See ADR-0012,
// "Self-contained per-resource entries, not a normalized range registry".
type ledgerEntry struct {
	// RangeKey is the canonical (Masked()) CIDR this entry allocates from,
	// e.g. "203.0.113.0/24". Entries sharing a RangeKey compete for the
	// same address pool.
	RangeKey string `json:"range_key"`

	// PrefixLength is RangeKey's own prefix length, copied out for
	// convenience when building the in-guest New-NetIPAddress call.
	PrefixLength int `json:"prefix_length"`

	DefaultGateway string   `json:"default_gateway,omitempty"`
	DNSServers     []string `json:"dns_servers,omitempty"`

	// AssignedAddress is empty until Allocate reserves one (see
	// reserveAddress). A non-empty value here is what makes a retry after
	// a crash idempotent: PersonalizeGuest reuses it rather than picking a
	// new address.
	AssignedAddress string `json:"assigned_address,omitempty"`
}

// ledgerData is the top-level structure persisted to ledgerFilename, keyed
// by resource ID.
type ledgerData struct {
	Entries map[string]*ledgerEntry `json:"entries"`
}

func newLedgerData() ledgerData {
	return ledgerData{Entries: make(map[string]*ledgerEntry)}
}

// normalizeLedgerData fills in defaults a decoded ledgerData might be
// missing — either because the file doesn't exist yet (diskjson.Store's
// newFunc already covers that) or because it was written before Entries
// existed, or was empty/partial. Mirrors devfactory's normalizeStoreData.
func normalizeLedgerData(d ledgerData) ledgerData {
	if d.Entries == nil {
		d.Entries = make(map[string]*ledgerEntry)
	}
	return d
}

// ledger returns this driver's IP allocation ledger store, lazily
// constructing an ephemeral temp-directory-backed one the first time it's
// needed if New wasn't used to set ledgerStore explicitly (e.g. a Driver
// built directly, as most existing tests in this package do). Safe for
// concurrent use.
func (d *Driver) ledger() *diskjson.Store[ledgerData] {
	d.ledgerOnce.Do(func() {
		if d.ledgerStore != nil {
			return
		}
		dir, err := os.MkdirTemp("", "boxy-hyperv-ledger-*")
		if err != nil {
			dir = os.TempDir()
		}
		d.ledgerStore = diskjson.New(filepath.Join(dir, ledgerFilename), newLedgerData)
	})
	return d.ledgerStore
}

// reserveRangeEntry records a new ledger entry for id from netCfg's
// range-mode fields. Called from Create after the VM is confirmed running
// and its ID resolved. No address is assigned yet — see ADR-0012,
// "Addresses are reserved at allocation time, not provision time".
func (d *Driver) reserveRangeEntry(id string, netCfg *NetworkConfig) error {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(netCfg.Range))
	if err != nil {
		return fmt.Errorf("invalid network.range %q: %w", netCfg.Range, err)
	}
	rangeKey := prefix.Masked().String()
	_, err = d.ledger().Update(func(data ledgerData) (ledgerData, error) {
		data = normalizeLedgerData(data)
		data.Entries[id] = &ledgerEntry{
			RangeKey:       rangeKey,
			PrefixLength:   prefix.Bits(),
			DefaultGateway: strings.TrimSpace(netCfg.DefaultGateway),
			DNSServers:     append([]string(nil), netCfg.DNSServers...),
		}
		return data, nil
	})
	return err
}

// reserveAddress returns id's assigned address, reserving the next free
// one in its ledger entry's range if none is assigned yet. Idempotent: a
// retry after a crash between reservation and the in-guest apply step in
// PersonalizeGuest returns the same address instead of burning a new one.
// The whole reserve-and-persist step runs inside one diskjson.Store.Update
// callback — which holds its lock across the entire load-modify-save, not
// just the accounting — so two concurrent callers reserving in the same
// range can never observe the same "next free" address.
func (d *Driver) reserveAddress(id string) (string, error) {
	var assigned string
	_, err := d.ledger().Update(func(data ledgerData) (ledgerData, error) {
		data = normalizeLedgerData(data)
		entry, ok := data.Entries[id]
		if !ok {
			return data, fmt.Errorf("no IP ledger entry for resource %s", id)
		}
		if entry.AssignedAddress != "" {
			assigned = entry.AssignedAddress
			return data, nil
		}
		taken := make(map[string]bool)
		if entry.DefaultGateway != "" {
			taken[entry.DefaultGateway] = true
		}
		for otherID, other := range data.Entries {
			if otherID == id || other.RangeKey != entry.RangeKey || other.AssignedAddress == "" {
				continue
			}
			taken[other.AssignedAddress] = true
		}
		addr, err := nextFreeAddress(entry.RangeKey, taken)
		if err != nil {
			return data, err
		}
		entry.AssignedAddress = addr
		assigned = addr
		return data, nil
	})
	if err != nil {
		return "", err
	}
	return assigned, nil
}

// ledgerLookup returns id's ledger entry, if any. ok is false with a nil
// error when id has no entry — the normal case for a resource created
// without network.range, not a failure.
func (d *Driver) ledgerLookup(id string) (*ledgerEntry, bool, error) {
	data, err := d.ledger().Load()
	if err != nil {
		return nil, false, err
	}
	entry, ok := data.Entries[id]
	return entry, ok, nil
}

// releaseAddress removes id's ledger entry, if any, freeing its address
// (if one was assigned) back to the range. Deleting the entry is the whole
// release: "taken" addresses are computed by scanning live entries sharing
// a RangeKey (see reserveAddress), not tracked as a separate free-list, so
// nothing else needs updating. A no-op if id has no entry — callers are
// not expected to check ledgerLookup first (see Driver.Delete).
func (d *Driver) releaseAddress(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	_, err := d.ledger().Update(func(data ledgerData) (ledgerData, error) {
		data = normalizeLedgerData(data)
		delete(data.Entries, id)
		return data, nil
	})
	return err
}

// nextFreeAddress returns the first IPv4 address in cidr (in ascending
// order, excluding the network and broadcast addresses) that isn't a key
// in taken. cidr must already be a valid, Masked() IPv4 CIDR string — see
// reserveRangeEntry, the only writer of a ledgerEntry's RangeKey.
func nextFreeAddress(cidr string, taken map[string]bool) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid range key %q: %w", cidr, err)
	}
	first, last, err := ipv4UsableBounds(prefix)
	if err != nil {
		return "", err
	}
	for addr := first; ; addr = addr.Next() {
		if !taken[addr.String()] {
			return addr.String(), nil
		}
		if addr == last {
			break
		}
	}
	return "", fmt.Errorf("no free addresses remain in range %q", cidr)
}

// ipv4UsableBounds returns the first and last usable host addresses within
// prefix — the network and broadcast addresses excluded, matching how
// static_ip and every real-world IPv4 subnet already reserve those two.
func ipv4UsableBounds(prefix netip.Prefix) (first, last netip.Addr, err error) {
	addr := prefix.Addr()
	if !addr.Is4() {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("network.range %q must be an IPv4 CIDR", prefix)
	}
	ones := prefix.Bits()
	b := addr.As4()
	val := binary.BigEndian.Uint32(b[:])
	var mask uint32
	if ones > 0 {
		mask = ^uint32(0) << (32 - ones)
	}
	network := val & mask
	broadcast := network | ^mask
	if broadcast-network < 2 {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("network.range %q is too small to contain any usable host addresses", prefix)
	}
	var fb, lb [4]byte
	binary.BigEndian.PutUint32(fb[:], network+1)
	binary.BigEndian.PutUint32(lb[:], broadcast-1)
	return netip.AddrFrom4(fb), netip.AddrFrom4(lb), nil
}
