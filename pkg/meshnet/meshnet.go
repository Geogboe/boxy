package meshnet

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// deviceLogger routes wireguard-go's own internal logging into log/slog
// instead of discarding it.
//
// This matters for diagnosing a mesh that reports success but passes no
// traffic: wireguard-go is silent by protocol design when it drops a
// handshake it can't authenticate, so without its verbose output there is
// no way to tell "the peer never received the packet" apart from "the peer
// received it and rejected it" -- which is exactly the ambiguity that left
// #372 un-root-caused. Its `Verbosef` stream carries the decisive lines
// ("Receiving handshake initiation from peer", "Invalid MAC of handshake",
// "Handshake for peer did not complete after 5 seconds, retrying").
//
// Routing to slog.Default() rather than a level constant deliberately
// reuses the logging boxy already has: `--log-level debug` turns this on,
// and on the daemon it also flows through diagnostics.NewHandler into
// `boxy diagnostics logs`. No new provider config surface, and an embedder
// outside boxy gets whatever their own slog default is.
//
// The interface name goes in the message text, not a structured attr,
// because the daemon's durable diagnostics path keeps a strict field
// allowlist (diagnostics.safeField) that has no "interface" field -- an
// attr would render fine under a plain text handler and then be silently
// dropped in `boxy diagnostics logs`, leaving several concurrent sandbox
// interfaces indistinguishable there. The message is always preserved, so
// prefixing it keeps per-interface identity on every path. This is also
// what wireguard-go's own NewLogger does with its `prepend` argument.
func deviceLogger(ifName string) *device.Logger {
	return &device.Logger{
		Verbosef: func(format string, args ...any) {
			// Guard before Sprintf: wireguard-go calls Verbosef per
			// handshake and keepalive, so formatting unconditionally
			// would cost on every event even with debug logging off.
			if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
				return
			}
			slog.Debug(ifName+": "+fmt.Sprintf(format, args...), "component", "meshnet")
		},
		Errorf: func(format string, args ...any) {
			slog.Error(ifName+": "+fmt.Sprintf(format, args...), "component", "meshnet")
		},
	}
}

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

	dev := device.NewDevice(tunDevice, conn.NewDefaultBind(), deviceLogger(ifName))
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

// probeInterfaceName is the fixed name Probe creates and immediately tears
// down. It is never exposed to a caller or persisted anywhere, so a single
// literal name is fine -- unlike New, Probe has no per-sandbox identity to
// derive a name from.
const probeInterfaceName = "boxy-probe"

// newProbeDevice is Probe's device constructor, package-level so tests can
// substitute a fake that never touches a real OS TUN device or kernel link
// state (New's bringLinkUp needs CAP_NET_ADMIN itself, the same thing Probe
// exists to check) -- production always uses the zero value, which resolves
// to New.
var newProbeDevice = New

// Probe reports whether this process can create a WireGuard device on this
// host right now: it creates one throwaway interface (listening on an
// ephemeral port, so it can never collide with a real segment's listener)
// and closes it immediately.
//
// This is the exact code path AddMeshPeering/MeshIdentity use in production
// (same tun.CreateTUN, same device.NewDevice) -- so a nil return here is
// real evidence the environment supports it (CAP_NET_ADMIN on Linux,
// wintun.dll present on Windows), not just an optimistic guess. Callers use
// this once at agent startup, gated on the mesh overlay being explicitly
// enabled (see docs/adr/0022's 2026-09-25 changelog entry): on Windows,
// creating the first Wintun adapter is what installs the kernel driver, so
// probing unconditionally would install it on every agent whether or not
// the operator ever intends to use cross-host mesh.
func Probe() error {
	iface, err := newProbeDevice(probeInterfaceName, 0)
	if err != nil {
		return err
	}
	return iface.Close()
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
