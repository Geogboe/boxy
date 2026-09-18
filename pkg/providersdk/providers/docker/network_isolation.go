package docker

import (
	"context"
	"fmt"
	"sort"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// segmentNetworkPrefix namespaces the per-sandbox networks this driver
// creates, so they are distinguishable from unrelated networks on the same
// Docker host by name as well as by managedLabel. The sandbox ID is appended
// verbatim: per providersdk.NetworkIsolator.CreateSegment's contract, callers
// supply an ID already safe for use in provider-specific resource names.
const segmentNetworkPrefix = "boxy-sb-"

// CreateSegment creates a dedicated bridge network for one sandbox. No
// explicit IPAM/subnet is set -- the Docker daemon auto-assigns a
// non-overlapping subnet from its own default address pools, so unlike
// Hyper-V's implementation there is no collision-avoidance ledger to
// maintain here.
//
// Idempotent per sandbox ID, as the interface requires: the deterministic
// network name is resolved first, and an existing network's ID is returned
// unchanged rather than issuing a second NetworkCreate. Without that check a
// retry would either fail on the duplicate name or leave a second, orphaned
// network behind that nothing ever tears down.
func (d *Driver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	name := segmentNetworkPrefix + sandboxID
	id, found, err := d.findSegmentNetwork(ctx, name)
	if err != nil {
		return "", err
	}
	if found {
		return providersdk.SegmentRef(id), nil
	}
	resp, err := d.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{managedLabel: managedLabelValue},
	})
	if err != nil {
		// A concurrent CreateSegment for the same sandbox can win the race
		// between the lookup above and this call; the daemon then rejects
		// the duplicate name. Re-resolve before reporting failure so the
		// loser of that race still returns the same segment as the winner.
		if id, found, lookupErr := d.findSegmentNetwork(ctx, name); lookupErr == nil && found {
			return providersdk.SegmentRef(id), nil
		}
		return "", fmt.Errorf("create docker network %q: %w", name, err)
	}
	return providersdk.SegmentRef(resp.ID), nil
}

// findSegmentNetwork resolves a segment network by its deterministic name.
// found is false with a nil error when no such network exists -- the normal
// first-call case, not a failure.
//
// A name match alone is not enough: the deterministic name is predictable,
// so an unrelated, unmanaged network happening to share it would otherwise
// be silently adopted as this sandbox's segment -- and later torn down by
// DestroySegment, which is not this driver's to remove. The managed label
// CreateSegment sets is checked before a found network is trusted.
func (d *Driver) findSegmentNetwork(ctx context.Context, name string) (id string, found bool, err error) {
	inspect, err := d.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect docker network %q: %w", name, err)
	}
	if inspect.Labels[managedLabel] != managedLabelValue {
		return "", false, fmt.Errorf("network %q already exists and is not a boxy-managed network", name)
	}
	return inspect.ID, true, nil
}

// AttachToSegment moves an already-running container onto the sandbox's
// network: it connects the container to the segment and then disconnects it
// from every other network it was on. The disconnect half is what actually
// delivers the isolation -- Driver.Create attaches new containers to the
// daemon's default bridge, so connecting alone would leave the container
// multi-homed on both the shared bridge and the sandbox's private network,
// reachable from (and able to reach) every other container on that host.
//
// Connect runs before disconnect, deliberately: the reverse order would leave
// the container with no network at all if the connect then failed.
//
// No EndpointSettings are specified -- Docker assigns an address from the
// network's own auto-assigned subnet.
//
// Idempotent, as the interface requires: a container already on the target
// network skips the connect (which the daemon would reject as a conflict) and
// still runs the disconnect sweep, so a retry after a partial attach
// converges on the same end state.
func (d *Driver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	info, err := d.cli.ContainerInspect(ctx, providerResourceID)
	if err != nil {
		return fmt.Errorf("inspect container %q: %w", providerResourceID, err)
	}

	// NetworkSettings.Networks is keyed by network *name*, while a
	// SegmentRef is the network *ID* CreateSegment returned, so the target
	// is recognized by either -- the map key or the endpoint's own NetworkID.
	// Disconnects go out by map key, since that is the name the daemon
	// expects.
	alreadyAttached := false
	var detach []string
	if info.NetworkSettings != nil {
		for name, endpoint := range info.NetworkSettings.Networks {
			if name == string(ref) || (endpoint != nil && endpoint.NetworkID == string(ref)) {
				alreadyAttached = true
				continue
			}
			detach = append(detach, name)
		}
	}

	if !alreadyAttached {
		if err := d.cli.NetworkConnect(ctx, string(ref), providerResourceID, nil); err != nil {
			return fmt.Errorf("connect container %q to segment %q: %w", providerResourceID, ref, err)
		}
	}

	// Map iteration order is random; sort so repeated runs (and tests)
	// observe a stable sequence of calls.
	sort.Strings(detach)
	for _, name := range detach {
		if err := d.cli.NetworkDisconnect(ctx, name, providerResourceID, false); err != nil {
			// Already gone (another attach raced this one, or the network
			// was removed underneath us) is the desired end state.
			if cerrdefs.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("disconnect container %q from network %q: %w", providerResourceID, name, err)
		}
	}
	return nil
}

// DestroySegment removes the network created by CreateSegment. Idempotent
// for an already-gone network, matching Driver.Delete's contract.
func (d *Driver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	if err := d.cli.NetworkRemove(ctx, string(ref)); err != nil {
		if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove docker network %q: %w", ref, err)
		}
	}
	return d.closeMeshInterface(ref)
}
