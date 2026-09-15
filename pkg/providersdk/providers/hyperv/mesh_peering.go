package hyperv

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/meshnet"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

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
func (d *Driver) AddMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	return iface.AddPeer(peerPublicKey, peerEndpoint, []string{peerCIDR})
}

// RemoveMeshPeer satisfies providersdk.MeshPeerer.
func (d *Driver) RemoveMeshPeer(_ context.Context, ref providersdk.SegmentRef, peerPublicKey string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	return iface.RemovePeer(peerPublicKey)
}

func (d *Driver) meshInterfaceFor(ref providersdk.SegmentRef) (*meshnet.Interface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	if d.meshInterfaces == nil {
		d.meshInterfaces = make(map[providersdk.SegmentRef]*meshnet.Interface)
	}
	if iface, ok := d.meshInterfaces[ref]; ok {
		return iface, nil
	}
	iface, err := meshnet.New(string(ref), 0)
	if err != nil {
		return nil, fmt.Errorf("create mesh interface for segment %q: %w", ref, err)
	}
	d.meshInterfaces[ref] = iface
	return iface, nil
}

func (d *Driver) existingMeshInterface(ref providersdk.SegmentRef) (*meshnet.Interface, error) {
	d.meshMu.Lock()
	defer d.meshMu.Unlock()
	iface, ok := d.meshInterfaces[ref]
	if !ok {
		return nil, fmt.Errorf("no mesh interface for segment %q -- MeshIdentity must be called for this segment before AddMeshPeer/RemoveMeshPeer", ref)
	}
	return iface, nil
}

// closeMeshInterfaces closes every live mesh interface this driver owns.
// Test-only for now (referenced from mesh_peering_test.go); DestroySegment
// should call this for a specific ref once a segment is torn down -- left
// as a follow-up wiring note for whichever change next touches
// DestroySegment, not built as part of this plan's scope.
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
