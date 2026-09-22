package meshnet

import (
	"strings"
	"testing"
)

func TestSafeInterfaceName_PassesThroughShortIDs(t *testing.T) {
	if got := SafeInterfaceName("boxy-sb-abc123", 15); got != "boxy-sb-abc123" {
		t.Fatalf("SafeInterfaceName = %q, want unchanged short id", got)
	}
}

func TestSafeInterfaceName_TruncatesLongIDsDeterministically(t *testing.T) {
	// A real Docker network ID: 64 hex characters, the exact shape that
	// failed TUN creation with "invalid argument" on a real Linux host.
	id := strings.Repeat("a", 64)
	got := SafeInterfaceName(id, MaxLinuxInterfaceName)
	if len(got) > MaxLinuxInterfaceName {
		t.Fatalf("SafeInterfaceName = %q, length %d exceeds max %d", got, len(got), MaxLinuxInterfaceName)
	}
	again := SafeInterfaceName(id, MaxLinuxInterfaceName)
	if got != again {
		t.Fatalf("SafeInterfaceName is not deterministic: %q != %q", got, again)
	}
	otherID := strings.Repeat("b", 64)
	if other := SafeInterfaceName(otherID, MaxLinuxInterfaceName); other == got {
		t.Fatalf("different ids collided on the same safe name %q", got)
	}
}

func TestSafeInterfaceName_RespectsMaxLen(t *testing.T) {
	for _, maxLen := range []int{1, 2, 3, 15, 32} {
		got := SafeInterfaceName(strings.Repeat("x", 100), maxLen)
		if len(got) > maxLen {
			t.Fatalf("maxLen=%d: SafeInterfaceName = %q, length %d exceeds max", maxLen, got, len(got))
		}
	}
}
