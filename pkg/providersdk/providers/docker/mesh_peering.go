package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/meshnet"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// meshInterface is the subset of *meshnet.Interface's API this driver
// needs. Abstracted behind an interface (rather than using *meshnet.Interface
// directly) so tests can inject a fake instead of creating a real OS TUN
// device and performing an actual WireGuard handshake -- mirroring
// hyperv.Driver's identical seam.
type meshInterface interface {
	PublicKeyHex() string
	Name() (string, error)
	AddPeer(pubKeyHex, endpoint string, allowedIPs []string) error
	RemovePeer(pubKeyHex string) error
	Close() error
}

var _ meshInterface = (*meshnet.Interface)(nil)

// MeshIdentity satisfies providersdk.MeshPeerer. Unlike Hyper-V (which
// tracks its own segment CIDR in a ledger, since it chose the subnet
// itself), this driver lets Docker auto-assign the subnet at CreateSegment
// time (Plan 1a) -- so discovering it means asking Docker directly via
// NetworkInspect.
func (d *Driver) MeshIdentity(ctx context.Context, ref providersdk.SegmentRef) (string, string, string, error) {
	inspected, err := d.cli.NetworkInspect(ctx, string(ref), network.InspectOptions{})
	if err != nil {
		return "", "", "", fmt.Errorf("inspect network %q: %w", ref, err)
	}
	if len(inspected.IPAM.Config) == 0 || inspected.IPAM.Config[0].Subnet == "" {
		return "", "", "", fmt.Errorf("network %q has no discoverable subnet", ref)
	}
	cidr := inspected.IPAM.Config[0].Subnet

	iface, err := d.meshInterfaceFor(ref)
	if err != nil {
		return "", "", "", err
	}
	ifName, err := iface.Name()
	if err != nil {
		return "", "", "", fmt.Errorf("get mesh interface name for segment %q: %w", ref, err)
	}
	if _, err := d.runHost(ctx, "ip", "route", "add", cidr, "dev", ifName); err != nil {
		return "", "", "", fmt.Errorf("route segment %q's subnet through mesh interface: %w", ref, err)
	}
	return iface.PublicKeyHex(), d.meshEndpoint, cidr, nil
}

// AddMeshPeer satisfies providersdk.MeshPeerer. The segment's mesh
// interface must already exist (created by a prior MeshIdentity call).
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
		factory = func(ifName string) (meshInterface, error) { return meshnet.New(ifName, 0) }
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

// closeMeshInterfaces closes every live mesh interface this driver owns.
// Test-only for now -- see hyperv's identical method for the same
// DestroySegment-wiring follow-up note.
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
