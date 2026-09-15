package meshnet

import (
	"fmt"

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
	return newWithTUNFactory(ifName, listenPort, func(name string, mtu int) (tun.Device, error) {
		return tun.CreateTUN(name, mtu)
	})
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
