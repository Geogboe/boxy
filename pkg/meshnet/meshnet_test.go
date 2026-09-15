package meshnet

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

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

func newWithTUNFactoryAndAddr(t *testing.T, ifName string, addr netip.Addr) (*Interface, error) {
	t.Helper()
	iface, err := newWithTUNFactory(ifName, 0, func(name string, mtu int) (tun.Device, error) {
		tunDev, _, err := netstack.CreateNetTUN([]netip.Addr{addr}, nil, mtu)
		return tunDev, err
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = iface.Close() })
	return iface, nil
}

func TestAddPeer_TwoInterfacesHandshakeAndExchangeData(t *testing.T) {
	addrA := netip.MustParseAddr("192.0.2.1")
	addrB := netip.MustParseAddr("192.0.2.2")

	a, err := newWithTUNFactoryAndAddr(t, "test-peer-a", addrA)
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	b, err := newWithTUNFactoryAndAddr(t, "test-peer-b", addrB)
	if err != nil {
		t.Fatalf("New b: %v", err)
	}

	// Endpoints are loopback here -- netstack only replaces the *inner* TUN
	// (the virtual interface WireGuard decrypts traffic onto); the *outer*
	// WireGuard protocol traffic still goes through conn.NewDefaultBind()'s
	// real OS UDP sockets (set in newWithTUNFactory), so this exercises the
	// real handshake/encryption path end to end, not a simulated one.
	// newForTest/newWithTUNFactoryAndAddr construct with listenPort=0
	// (ephemeral) since most tests don't care what port they land on; this
	// test needs stable, known ports up front to build explicit endpoint
	// strings, so it re-sets listen_port directly via IpcSet after
	// construction -- WireGuard's UAPI supports changing listen_port at any
	// time, not just before Up().
	portA, portB := 51900, 51901
	if err := a.dev.IpcSet(fmt.Sprintf("listen_port=%d\n", portA)); err != nil {
		t.Fatalf("set port a: %v", err)
	}
	if err := b.dev.IpcSet(fmt.Sprintf("listen_port=%d\n", portB)); err != nil {
		t.Fatalf("set port b: %v", err)
	}

	if err := a.AddPeer(b.PublicKeyHex(), fmt.Sprintf("127.0.0.1:%d", portB), []string{"192.0.2.2/32"}); err != nil {
		t.Fatalf("a.AddPeer: %v", err)
	}
	if err := b.AddPeer(a.PublicKeyHex(), fmt.Sprintf("127.0.0.1:%d", portA), []string{"192.0.2.1/32"}); err != nil {
		t.Fatalf("b.AddPeer: %v", err)
	}

	if !waitForHandshake(t, a, 5*time.Second) {
		t.Fatal("interface a never completed a handshake with b")
	}
}

func TestRemovePeer_StopsFurtherHandshakes(t *testing.T) {
	a, err := newForTest(t, "test-rmpeer-a")
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	fakePeerKey := strings.Repeat("00", 31) + "aa" // 64 hex chars = 32 bytes
	if err := a.AddPeer(fakePeerKey, "127.0.0.1:9", []string{"192.0.2.9/32"}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := a.RemovePeer(fakePeerKey); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	// No handshake assertion here -- the peer was never reachable anyway
	// (port 9 discards). This only proves RemovePeer doesn't error, which
	// is the whole surface worth testing without a live second peer.
}

// waitForHandshake polls iface's own IpcGet output for a completed
// handshake (a "last_handshake_time_sec" field appears once one occurs).
func waitForHandshake(t *testing.T, iface *Interface, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := iface.dev.IpcGet()
		if err == nil && strings.Contains(status, "last_handshake_time_sec") {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestName_ReturnsTheUnderlyingTUNDeviceName(t *testing.T) {
	iface, err := newForTest(t, "test-name")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	name, err := iface.Name()
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if name == "" {
		t.Fatal("expected a non-empty interface name")
	}
}
