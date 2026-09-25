package segmentcidr

import (
	"strings"
	"testing"
)

func TestNewDefault(t *testing.T) {
	a := NewDefault()
	if got, want := a.Count(), 8192; got != want {
		t.Fatalf("Count() = %d, want %d (a /16 yields that many /29s)", got, want)
	}
}

func TestAllocate_FirstBlockWhenNothingInUse(t *testing.T) {
	got, err := NewDefault().Allocate(nil, nil)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if want := "10.250.0.0/29"; got != want {
		t.Fatalf("Allocate() = %q, want %q", got, want)
	}
}

func TestAllocate_SkipsInUseBlocks(t *testing.T) {
	got, err := NewDefault().Allocate([]string{"10.250.0.0/29", "10.250.0.8/29"}, nil)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if want := "10.250.0.16/29"; got != want {
		t.Fatalf("Allocate() = %q, want %q", got, want)
	}
}

// TestAllocate_SkipsAvoided covers the host-refusal retry path: a range the
// host rejected as locally conflicting must not be proposed again on the
// immediate retry, even though nothing has it "in use" from the caller's
// point of view.
func TestAllocate_SkipsAvoided(t *testing.T) {
	got, err := NewDefault().Allocate(nil, []string{"10.250.0.0/29"})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if want := "10.250.0.8/29"; got != want {
		t.Fatalf("Allocate() = %q, want %q", got, want)
	}
}

// TestAllocate_DetectsOverlapWithLargerPrefix is the case a naive
// "is this exact string taken" check would miss: an in-use /16 swallows
// every /29 inside it, so the allocator has to compare ranges, not strings.
func TestAllocate_DetectsOverlapWithLargerPrefix(t *testing.T) {
	got, err := NewDefault().Allocate([]string{"10.250.0.0/20"}, nil)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if want := "10.250.16.0/29"; got != want {
		t.Fatalf("Allocate() = %q, want %q (must skip the whole /20)", got, want)
	}
}

// TestAllocate_IgnoresUnparsableEntries pins the conservative direction: a
// malformed persisted record must not make segment creation impossible.
func TestAllocate_IgnoresUnparsableEntries(t *testing.T) {
	got, err := NewDefault().Allocate([]string{"", "not-a-cidr", "10.250.0.0/29"}, nil)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if want := "10.250.0.8/29"; got != want {
		t.Fatalf("Allocate() = %q, want %q", got, want)
	}
}

func TestAllocate_ExhaustionIsAnError(t *testing.T) {
	a, err := New("10.250.0.0/30", 31)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.Allocate([]string{"10.250.0.0/31", "10.250.0.2/31"}, nil); err == nil {
		t.Fatal("expected exhaustion error when every block is taken")
	} else if !strings.Contains(err.Error(), "no free") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocate_BlocksAreDistinctAcrossManyAllocations(t *testing.T) {
	a := NewDefault()
	seen := map[string]bool{}
	var inUse []string
	for i := 0; i < 50; i++ {
		got, err := a.Allocate(inUse, nil)
		if err != nil {
			t.Fatalf("Allocate #%d: %v", i, err)
		}
		if seen[got] {
			t.Fatalf("Allocate returned %q twice", got)
		}
		seen[got] = true
		inUse = append(inUse, got)
	}
}

func TestNew_RejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		base     string
		blockLen int
	}{
		{"not-a-cidr", 29},
		{"2001:db8::/32", 40}, // IPv6
		{"10.250.0.0/16", 8},  // block bigger than base
		{"10.250.0.0/16", 33}, // beyond IPv4
	} {
		if _, err := New(tc.base, tc.blockLen); err == nil {
			t.Fatalf("New(%q, %d) succeeded, want error", tc.base, tc.blockLen)
		}
	}
}
