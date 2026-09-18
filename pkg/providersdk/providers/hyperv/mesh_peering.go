package hyperv

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/meshnet"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// meshInterface is the subset of *meshnet.Interface's API this driver
// needs. Abstracted behind an interface (rather than using *meshnet.Interface
// directly) so tests can inject a fake instead of creating a real OS TUN
// device and performing an actual WireGuard handshake -- mirroring this
// package's existing fake-guest-executor pattern for PowerShell Direct/SSH.
type meshInterface interface {
	PublicKeyHex() string
	Name() (string, error)
	AddPeer(pubKeyHex, endpoint string, allowedIPs []string) error
	RemovePeer(pubKeyHex string) error
	Close() error
}

var _ meshInterface = (*meshnet.Interface)(nil)

// MeshIdentity satisfies providersdk.MeshPeerer. It lazily creates this
// segment's meshnet.Interface on first call, routes the segment's own
// subnet through it (New-NetRoute -- see Plan 2b's Global Constraints on
// this command being unverified against a live host), and returns what a
// remote peer needs to connect: this interface's public key, this driver's
// configured mesh endpoint, and the segment's own CIDR (so the *other* side
// knows what to route through this peer once it calls AddMeshPeer back).
func (d *Driver) MeshIdentity(ctx context.Context, ref providersdk.SegmentRef) (string, string, string, error) {
	alloc, ok, err := d.segments().lookupBySwitchName(string(ref))
	if err != nil {
		return "", "", "", fmt.Errorf("look up segment %q: %w", ref, err)
	}
	if !ok {
		return "", "", "", fmt.Errorf("segment %q not found", ref)
	}
	iface, err := d.meshInterfaceFor(ref)
	if err != nil {
		return "", "", "", err
	}
	ifName, err := iface.Name()
	if err != nil {
		return "", "", "", fmt.Errorf("get mesh interface name for segment %q: %w", ref, err)
	}
	if _, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (-not (Get-NetRoute -InterfaceAlias '%s' -DestinationPrefix '%s' -ErrorAction SilentlyContinue)) {
    New-NetRoute -InterfaceAlias '%s' -DestinationPrefix '%s' | Out-Null
}
`, psq(ifName), psq(alloc.CIDR), psq(ifName), psq(alloc.CIDR))); err != nil {
		return "", "", "", fmt.Errorf("route segment %q's subnet through mesh interface: %w", ref, err)
	}
	return iface.PublicKeyHex(), d.meshEndpoint, alloc.CIDR, nil
}

// AddMeshPeer satisfies providersdk.MeshPeerer. The segment's mesh
// interface must already exist (created by a prior MeshIdentity call) --
// there is no lazy-create path here, since a caller adding a peer without
// first asking for this side's own identity is a genuine ordering bug, not
// something to paper over.
func (d *Driver) AddMeshPeer(ctx context.Context, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	if err := iface.AddPeer(peerPublicKey, peerEndpoint, []string{peerCIDR}); err != nil {
		return err
	}
	// AllowedIPs above only configures WireGuard's own crypto-routing; it
	// does not make the kernel hand cross-host traffic to this interface in
	// the first place -- the identical bug fixed for the Docker driver (see
	// docker.Driver.AddMeshPeer's comment). A repeat call for an
	// already-routed peer is tolerated as a legitimate outcome, not an error.
	ifName, err := iface.Name()
	if err != nil {
		return fmt.Errorf("get mesh interface name for segment %q: %w", ref, err)
	}
	if _, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (-not (Get-NetRoute -InterfaceAlias '%s' -DestinationPrefix '%s' -ErrorAction SilentlyContinue)) {
    New-NetRoute -InterfaceAlias '%s' -DestinationPrefix '%s' | Out-Null
}
`, psq(ifName), psq(peerCIDR), psq(ifName), psq(peerCIDR))); err != nil {
		return fmt.Errorf("route peer subnet %q through mesh interface: %w", peerCIDR, err)
	}
	return nil
}

// RemoveMeshPeer satisfies providersdk.MeshPeerer.
func (d *Driver) RemoveMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	return iface.RemovePeer(peerPublicKey)
}

func (d *Driver) meshInterfaceFor(ref providersdk.SegmentRef) (meshInterface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	if d.meshInterfaces == nil {
		d.meshInterfaces = make(map[providersdk.SegmentRef]meshInterface)
	}
	if iface, ok := d.meshInterfaces[ref]; ok {
		return iface, nil
	}
	factory := d.newMeshInterface
	if factory == nil {
		factory = func(ifName string) (meshInterface, error) {
			// The interface must bind the exact port MeshIdentity advertises
			// as d.meshEndpoint -- see meshnet.ListenPortFromEndpoint's doc
			// comment for why listen port 0 breaks every handshake.
			port, err := meshnet.ListenPortFromEndpoint(d.meshEndpoint)
			if err != nil {
				return nil, fmt.Errorf("resolve listen port from mesh endpoint: %w", err)
			}
			return meshnet.New(ifName, port)
		}
	}
	iface, err := factory(string(ref))
	if err != nil {
		return nil, fmt.Errorf("create mesh interface for segment %q: %w", ref, err)
	}
	d.meshInterfaces[ref] = iface
	return iface, nil
}

func (d *Driver) existingMeshInterface(ref providersdk.SegmentRef) (meshInterface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	iface, ok := d.meshInterfaces[ref]
	if !ok {
		return nil, fmt.Errorf("no mesh interface for segment %q -- MeshIdentity must be called for this segment before AddMeshPeer/RemoveMeshPeer", ref)
	}
	return iface, nil
}

// closeMeshInterface closes and forgets the mesh interface for one segment,
// if one was ever created for it. Called from DestroySegment so a torn-down
// sandbox's WireGuard/TUN interface never outlives it -- without this, every
// cross-host sandbox leaked an OS interface for the life of the daemon.
// A no-op (nil error) when no mesh interface exists for ref, matching this
// package's Delete/Destroy idempotency convention.
func (d *Driver) closeMeshInterface(ref providersdk.SegmentRef) error {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	iface, ok := d.meshInterfaces[ref]
	if !ok {
		return nil
	}
	if err := iface.Close(); err != nil {
		return fmt.Errorf("close mesh interface for segment %q: %w", ref, err)
	}
	delete(d.meshInterfaces, ref)
	return nil
}

// closeMeshInterfaces closes every live mesh interface this driver owns.
// Test-only; DestroySegment uses the single-ref closeMeshInterface above.
func (d *Driver) closeMeshInterfaces() error {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	for ref, iface := range d.meshInterfaces {
		if err := iface.Close(); err != nil {
			return fmt.Errorf("close mesh interface for segment %q: %w", ref, err)
		}
		delete(d.meshInterfaces, ref)
	}
	return nil
}
