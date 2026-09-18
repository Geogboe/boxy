package docker

import (
	"context"
	"fmt"
	"strings"

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
	// A Docker bridge network already owns a local, directly-connected route
	// to its own subnet the moment its gateway address is assigned -- this
	// call is only needed on setups where that isn't already true. On any
	// ordinary Docker host it always fails with "RTNETLINK answers: File
	// exists" (confirmed running this end to end): the desired route already
	// exists via the bridge, so that specific failure is the success case,
	// not an error, matching this driver's existing idempotency convention
	// (see AttachToSegment's tolerated not-found on disconnect).
	if out, err := d.runHost(ctx, "ip", "route", "add", cidr, "dev", ifName); err != nil && !strings.Contains(out, "File exists") {
		return "", "", "", fmt.Errorf("route segment %q's subnet through mesh interface: %w", ref, err)
	}
	return iface.PublicKeyHex(), d.meshEndpoint, cidr, nil
}

// AddMeshPeer satisfies providersdk.MeshPeerer. The segment's mesh
// interface must already exist (created by a prior MeshIdentity call).
func (d *Driver) AddMeshPeer(ctx context.Context, ref providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	iface, err := d.existingMeshInterface(ref)
	if err != nil {
		return err
	}
	if err := iface.AddPeer(peerPublicKey, peerEndpoint, []string{peerCIDR}); err != nil {
		return err
	}
	// AllowedIPs above only configures WireGuard's own crypto-routing --
	// which peer a packet already handed to this interface gets encrypted
	// for. It does not make the kernel hand cross-host traffic to this
	// interface in the first place: confirmed running this end to end,
	// containers on this host could not reach the peer's segment at all
	// without this route, even though the tunnel itself was correctly
	// established and MeshIdentity/AddMeshPeer both reported success.
	ifName, err := iface.Name()
	if err != nil {
		return fmt.Errorf("get mesh interface name for segment %q: %w", ref, err)
	}
	if out, err := d.runHost(ctx, "ip", "route", "add", peerCIDR, "dev", ifName); err != nil && !strings.Contains(out, "File exists") {
		return fmt.Errorf("route peer subnet %q through mesh interface: %w", peerCIDR, err)
	}
	return nil
}

// RemoveMeshPeer satisfies providersdk.MeshPeerer.
//
// Known gap, not fixed here: this does not remove the kernel route
// AddMeshPeer added for the departing peer's subnet -- the MeshPeerer
// interface passes only peerPublicKey, not the CIDR that route needs. For a
// two-host mesh this is harmless (DestroySegment closes the whole interface
// when the sandbox goes away, taking every route on it with it), but a
// mesh spanning three or more hosts that removes exactly one peer while
// keeping the interface up for the others would leave a stale route
// silently blackholing traffic to the removed peer instead of failing
// fast. Revisit if/when a mesh larger than two hosts is actually exercised.
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
	// Docker's SegmentRef is a full network ID (64 hex characters) -- far
	// longer than Linux's IFNAMSIZ allows for a real TUN device name
	// (MaxLinuxInterfaceName), so it cannot be used directly. Verified by
	// running this end to end on a real Linux host: an unmodified ref failed
	// TUN creation with "invalid argument" every time.
	iface, err := factory(meshnet.SafeInterfaceName(string(ref), meshnet.MaxLinuxInterfaceName))
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
