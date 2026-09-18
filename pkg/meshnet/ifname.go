package meshnet

import (
	"crypto/sha256"
	"encoding/hex"
)

// MaxLinuxInterfaceName is the longest name the Linux kernel accepts for a
// network interface: IFNAMSIZ is 16 bytes including the trailing NUL, so 15
// usable characters is the hard ceiling (verified by the "invalid argument"
// TUN-creation failure this constant was added to fix -- a raw 64-character
// Docker network ID used directly as a TUN device name).
const MaxLinuxInterfaceName = 15

// SafeInterfaceName derives an OS-safe network interface name from an
// arbitrary identifier, such as a driver's own SegmentRef. Platforms differ
// widely in how long an interface name may be (Linux's TUN/TAP driver caps
// at MaxLinuxInterfaceName; Windows' Wintun tolerates far longer names), so
// callers pass their own maxLen rather than this package assuming one.
//
// An id that already fits within maxLen passes through unchanged -- this is
// a no-op for identifiers that were already interface-name-shaped, which
// keeps existing short names (like Hyper-V's switch-name-derived refs)
// stable. A longer id is replaced with a short, deterministic name derived
// from its SHA-256 hash, so the same id always maps to the same interface
// name and a caller never needs to track the mapping separately.
func SafeInterfaceName(id string, maxLen int) string {
	if len(id) <= maxLen {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	hash := hex.EncodeToString(sum[:])
	const prefix = "bxm"
	if maxLen <= 0 {
		return ""
	}
	if maxLen <= len(prefix) {
		return prefix[:maxLen]
	}
	n := maxLen - len(prefix)
	if n > len(hash) {
		n = len(hash)
	}
	return prefix + hash[:n]
}
