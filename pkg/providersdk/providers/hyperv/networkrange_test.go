package hyperv

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// --- providersdk.NetworkRangeReporter interface compliance ---

var _ providersdk.NetworkRangeReporter = (*Driver)(nil)

func TestDriver_NetworkRanges_EmptySwitchName(t *testing.T) {
	d := mockDriver(nil)
	if _, err := d.NetworkRanges(context.Background(), "  "); err == nil {
		t.Fatal("expected error for empty switch name")
	}
}

func TestDriver_NetworkRanges_HappyPath_NATConfirmed(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if !strings.Contains(script, "vEthernet (boxy-switch)") {
			t.Fatalf("script does not target the switch's vEthernet alias:\n%s", script)
		}
		return "203.0.113.1|24|203.0.113.0/24\n", nil
	})

	ranges, err := d.NetworkRanges(context.Background(), "boxy-switch")
	if err != nil {
		t.Fatalf("NetworkRanges: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("len(ranges) = %d, want 1: %+v", len(ranges), ranges)
	}
	r := ranges[0]
	if r.CIDR != "203.0.113.0/24" {
		t.Errorf("CIDR = %q, want 203.0.113.0/24", r.CIDR)
	}
	if r.Gateway != "203.0.113.1" {
		t.Errorf("Gateway = %q, want 203.0.113.1", r.Gateway)
	}
	if !r.NATBacked {
		t.Error("NATBacked = false, want true (Get-NetNat prefix matched)")
	}
}

func TestDriver_NetworkRanges_HappyPath_NoNATMatch(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		// No NAT entries at all (third field empty) — a plain internal
		// switch with no NAT. 192.0.2.0/24 is TEST-NET-1 (RFC 5737), not a
		// private range, so it doesn't trip Betterleaks' PII scan.
		return "192.0.2.1|24|\n", nil
	})

	ranges, err := d.NetworkRanges(context.Background(), "internal-switch")
	if err != nil {
		t.Fatalf("NetworkRanges: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("len(ranges) = %d, want 1", len(ranges))
	}
	if ranges[0].NATBacked {
		t.Error("NATBacked = true, want false — no NAT prefix was reported")
	}
	if ranges[0].CIDR != "192.0.2.0/24" {
		t.Errorf("CIDR = %q, want 192.0.2.0/24", ranges[0].CIDR)
	}
}

func TestParseNetworkRanges_SkipsMalformedLinesButKeepsValidOnes(t *testing.T) {
	// Simulates a PowerShell property name this code guessed wrong about:
	// tolerant parsing must degrade a malformed line to "not discovered"
	// rather than panicking or producing a bogus CIDR, since the real
	// script ships unverified against actual Hyper-V (see ADR-0013).
	out := strings.Join([]string{
		"not-a-line-at-all",                // no separator at all
		"203.0.113.1|not-a-number|",        // non-numeric prefix length
		"2001:db8::1|32|",                  // IPv6 address (unsupported)
		"198.51.100.1|24|garbage-not-cidr", // unparseable NAT prefix
		"192.0.2.1|24|192.0.2.0/24",        // one fully valid line
	}, "\n")

	ranges := parseNetworkRanges(out)
	if len(ranges) != 2 {
		t.Fatalf("len(ranges) = %d, want 2: %+v", len(ranges), ranges)
	}

	// The 198.51.100.1 line has a garbage NAT prefix but is otherwise
	// valid: it should still parse, just with NATBacked left false.
	byCIDR := map[string]providersdk.NetworkRange{}
	for _, r := range ranges {
		byCIDR[r.CIDR] = r
	}
	garbage, ok := byCIDR["198.51.100.0/24"]
	if !ok {
		t.Fatalf("expected 198.51.100.0/24 to still parse despite its garbage NAT prefix; got %+v", ranges)
	}
	if garbage.NATBacked {
		t.Error("NATBacked = true, want false — the NAT prefix field was unparseable garbage")
	}

	valid, ok := byCIDR["192.0.2.0/24"]
	if !ok {
		t.Fatalf("expected 192.0.2.0/24 to parse; got %+v", ranges)
	}
	if !valid.NATBacked {
		t.Error("NATBacked = false, want true for the fully valid line")
	}
}

func TestDriver_NetworkRanges_NoAddressesFound(t *testing.T) {
	// A Private switch (or one with no host vNIC at all) — Get-NetIPAddress
	// returns nothing; this is not an error, just an empty, indeterminate
	// result for the caller to decide how to treat.
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "", nil
	})

	ranges, err := d.NetworkRanges(context.Background(), "private-switch")
	if err != nil {
		t.Fatalf("NetworkRanges: %v", err)
	}
	if ranges != nil {
		t.Fatalf("ranges = %+v, want nil", ranges)
	}
}

func TestDriver_NetworkRanges_QueryFailurePropagates(t *testing.T) {
	d := mockDriver(func(_ context.Context, _ string) (string, error) {
		return "", fmt.Errorf("Get-NetIPAddress failed")
	})
	if _, err := d.NetworkRanges(context.Background(), "boxy-switch"); err == nil {
		t.Fatal("expected the PowerShell error to propagate")
	}
}

// --- Create()'s validateNetworkRange wiring ---

func TestDriver_Create_RangeWithinDiscoveredSwitchRange_Succeeds(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Get-NetIPAddress"):
			// Switch's real range is a /24; the pool declares a /25
			// sub-range carved out of it — a legitimate, contained
			// configuration, not an equality match.
			return "203.0.113.1|24|203.0.113.0/24\n", nil
		default:
			return fakeGUID + "\n", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		Switch:      "boxy-switch",
		Network:     &NetworkConfig{Range: "203.0.113.128/25"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestDriver_Create_RangeOutsideDiscoveredSwitchRange_Fails(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Get-NetIPAddress"):
			return "203.0.113.1|24|203.0.113.0/24\n", nil
		default:
			return fakeGUID + "\n", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		Switch:      "boxy-switch",
		Network:     &NetworkConfig{Range: "198.51.100.0/24"},
	})
	if err == nil {
		t.Fatal("expected an error: declared range does not overlap the discovered switch range")
	}
	if !strings.Contains(err.Error(), "198.51.100.0/24") || !strings.Contains(err.Error(), "203.0.113.0/24") {
		t.Errorf("error %q should name both the declared and discovered ranges", err.Error())
	}
}

func TestDriver_Create_RangeValidation_IndeterminateDiscoveryFailureProceeds(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Get-NetIPAddress"):
			return "", fmt.Errorf("Get-NetIPAddress failed")
		default:
			return fakeGUID + "\n", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		Switch:      "boxy-switch",
		Network:     &NetworkConfig{Range: "203.0.113.0/24"},
	})
	if err != nil {
		t.Fatalf("Create should proceed on an indeterminate discovery failure, got: %v", err)
	}
}

func TestDriver_Create_RangeValidation_NoDiscoveredRangeProceeds(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Get-NetIPAddress"):
			return "", nil // Private switch: nothing discoverable.
		default:
			return fakeGUID + "\n", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		Switch:      "boxy-switch",
		Network:     &NetworkConfig{Range: "203.0.113.0/24"},
	})
	if err != nil {
		t.Fatalf("Create should proceed when nothing is discoverable, got: %v", err)
	}
}

func TestDriver_Create_StaticIPWithinDiscoveredSwitchRange_Succeeds(t *testing.T) {
	// static_ip mode is exactly as exposed to a typo/drifted address as
	// range mode — the same validation must cover it.
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Get-NetIPAddress"):
			return "203.0.113.1|24|203.0.113.0/24\n", nil
		default:
			return fakeGUID + "\n", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		Switch:      "boxy-switch",
		Network:     &NetworkConfig{StaticIP: "203.0.113.50"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestDriver_Create_StaticIPOutsideDiscoveredSwitchRange_Fails(t *testing.T) {
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		switch {
		case strings.Contains(script, hyperVAvailableMemoryScript):
			return "16384\n", nil
		case strings.Contains(script, "Get-NetIPAddress"):
			return "203.0.113.1|24|203.0.113.0/24\n", nil
		default:
			return fakeGUID + "\n", nil
		}
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		Switch:      "boxy-switch",
		Network:     &NetworkConfig{StaticIP: "198.51.100.50"},
	})
	if err == nil {
		t.Fatal("expected an error: declared static_ip does not fall within the discovered switch range")
	}
	if !strings.Contains(err.Error(), "198.51.100.50") || !strings.Contains(err.Error(), "203.0.113.0/24") {
		t.Errorf("error %q should name both the declared address and discovered range", err.Error())
	}
}

func TestDriver_Create_RangeValidation_SkippedWithoutSwitch(t *testing.T) {
	// No cc.Switch set — there is nothing to validate against, and this
	// must not call NetworkRanges at all (existing pools with no switch
	// declared, e.g. the pre-#223 test fixtures, must be unaffected).
	called := false
	d := mockDriver(func(_ context.Context, script string) (string, error) {
		if strings.Contains(script, "Get-NetIPAddress") {
			called = true
		}
		if strings.Contains(script, hyperVAvailableMemoryScript) {
			return "16384\n", nil
		}
		return fakeGUID + "\n", nil
	})

	_, err := d.Create(context.Background(), &CreateConfig{
		TemplateVHD: `C:\Templates\base.vhdx`,
		Network:     &NetworkConfig{Range: "203.0.113.0/24"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if called {
		t.Error("NetworkRanges was queried even though no switch was declared")
	}
}
