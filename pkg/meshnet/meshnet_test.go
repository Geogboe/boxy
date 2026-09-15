package meshnet

import (
	"encoding/hex"
	"net/netip"
	"testing"

	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func TestNew_GeneratesAKeypairAndBringsUpTheInterface(t *testing.T) {
	iface, err := newForTest(t, "test0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer iface.Close()

	pub := iface.PublicKeyHex()
	if pub == "" {
		t.Fatal("expected a non-empty public key")
	}
	raw, err := hex.DecodeString(pub)
	if err != nil {
		t.Fatalf("public key is not valid hex: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("public key length = %d, want 32 (Curve25519)", len(raw))
	}
}

func TestNew_TwoInterfacesGetDifferentKeypairs(t *testing.T) {
	a, err := newForTest(t, "test-a")
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	defer a.Close()
	b, err := newForTest(t, "test-b")
	if err != nil {
		t.Fatalf("New b: %v", err)
	}
	defer b.Close()

	if a.PublicKeyHex() == b.PublicKeyHex() {
		t.Fatal("two independently created interfaces must not share a keypair")
	}
}

func TestClose_IsIdempotent(t *testing.T) {
	iface, err := newForTest(t, "test-close")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := iface.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := iface.Close(); err != nil {
		t.Fatalf("second Close must be a no-op, got: %v", err)
	}
}

// newForTest builds an Interface backed by netstack's in-memory TUN instead
// of a real OS device -- no root/Administrator needed, portable to any CI
// runner. localAddr is an arbitrary address in a private test range; two
// interfaces in the same test must use different addresses so their
// netstacks don't collide.
func newForTest(t *testing.T, ifName string) (*Interface, error) {
	t.Helper()
	localAddr := netip.MustParseAddr("192.0.2.1")
	iface, err := newWithTUNFactory(ifName, 0, func(name string, mtu int) (tun.Device, error) {
		tunDev, _, err := netstack.CreateNetTUN([]netip.Addr{localAddr}, nil, mtu)
		return tunDev, err
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = iface.Close() })
	return iface, nil
}
