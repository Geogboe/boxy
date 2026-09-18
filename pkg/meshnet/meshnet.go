package meshnet

import (
	"fmt"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// Interface owns one WireGuard device for one sandbox segment. Never share
// one Interface across more than one sandbox -- create a new one per
// segment that needs cross-host connectivity.
type Interface struct {
	dev       *device.Device
	tunDevice tun.Device
	publicKey string // hex-encoded, safe to log/return

	closed bool
}

// tunFactory is the seam tests substitute to avoid needing a real OS TUN
// device (which needs root/Administrator and creates an actual kernel
// network interface). Production always uses the zero value, which resolves
// to tun.CreateTUN.
type tunFactory func(ifName string, mtu int) (tun.Device, error)

// New creates a new WireGuard interface named ifName, generates a fresh
// keypair for it (see generateKeypair -- the private key never leaves this
// package), and brings the interface up listening on listenPort (0 lets
// the OS/WireGuard choose an ephemeral port).
func New(ifName string, listenPort int) (*Interface, error) {
	iface, err := newWithTUNFactory(ifName, listenPort, tun.CreateTUN)
	if err != nil {
		return nil, err
	}
	// dev.Up() above only starts WireGuard's own packet-processing loop; it
	// never touches the interface's kernel link state (see bringLinkUp's
	// doc comment). newWithTUNFactory itself stays free of this -- tests
	// call it directly with a netstack-backed fake tun.Device that has no
	// real kernel interface to bring up at all.
	osName, err := iface.Name()
	if err != nil {
		_ = iface.Close()
		return nil, fmt.Errorf("get OS interface name for %q: %w", ifName, err)
	}
	if err := bringLinkUp(osName); err != nil {
		_ = iface.Close()
		return nil, fmt.Errorf("bring up interface %q: %w", osName, err)
	}
	return iface, nil
}

func newWithTUNFactory(ifName string, listenPort int, factory tunFactory) (*Interface, error) {
	tunDevice, err := factory(ifName, device.DefaultMTU)
	if err != nil {
		return nil, fmt.Errorf("create TUN device %q: %w", ifName, err)
	}

	priv, pub, err := generateKeypair()
	if err != nil {
		_ = tunDevice.Close()
		return nil, err
	}

	dev := device.NewDevice(tunDevice, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, ifName+": "))
	if err := dev.IpcSet(fmt.Sprintf("private_key=%x\nlisten_port=%d\n", [32]byte(priv), listenPort)); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure WireGuard device %q: %w", ifName, err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bring up WireGuard device %q: %w", ifName, err)
	}

	return &Interface{dev: dev, tunDevice: tunDevice, publicKey: hexEncode(pub)}, nil
}

// PublicKeyHex returns this interface's public key, hex-encoded -- the only
// key material safe to share with a peer or log.
func (i *Interface) PublicKeyHex() string {
	return i.publicKey
}

// AddPeer adds (or replaces, if pubKeyHex is already configured) a peer on
// this interface: endpoint is the peer's "host:port" UDP address, and
// allowedIPs are the CIDRs this peer is allowed to send/receive traffic for.
// replace_allowed_ips=true means a re-add fully replaces the peer's prior
// allowed-IP set rather than appending to it, matching Boxy's segment
// membership being the sole source of truth for peer routing.
func (i *Interface) AddPeer(pubKeyHex, endpoint string, allowedIPs []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "public_key=%s\n", pubKeyHex)
	fmt.Fprintf(&b, "endpoint=%s\n", endpoint)
	b.WriteString("replace_allowed_ips=true\n")
	for _, ip := range allowedIPs {
		fmt.Fprintf(&b, "allowed_ip=%s\n", ip)
	}
	if err := i.dev.IpcSet(b.String()); err != nil {
		return fmt.Errorf("add peer %s: %w", pubKeyHex, err)
	}
	return nil
}

// RemovePeer removes the peer identified by pubKeyHex. Idempotent -- removing
// an unknown peer is not an error, matching this codebase's Delete/Destroy
// idempotency convention elsewhere.
func (i *Interface) RemovePeer(pubKeyHex string) error {
	cfg := fmt.Sprintf("public_key=%s\nremove=true\n", pubKeyHex)
	if err := i.dev.IpcSet(cfg); err != nil {
		return fmt.Errorf("remove peer %s: %w", pubKeyHex, err)
	}
	return nil
}

// Name returns the OS-level name this interface was actually given (which
// may differ from the ifName passed to New on some platforms). A driver's
// NetworkIsolator implementation uses this to route its segment's subnet
// through this specific interface -- pkg/meshnet itself never touches OS
// routing tables.
func (i *Interface) Name() (string, error) {
	return i.tunDevice.Name()
}

// Close tears down the WireGuard device and its TUN device. Idempotent --
// a second Close call is a no-op, matching this codebase's Delete/Destroy
// idempotency convention elsewhere (providersdk.Driver.Delete,
// providersdk.NetworkIsolator.DestroySegment).
func (i *Interface) Close() error {
	if i.closed {
		return nil
	}
	i.closed = true
	i.dev.Close() // device.Device.Close also closes the underlying tun.Device
	return nil
}
