package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/segmentcidr"
)

// segmentNetworkPrefix namespaces the per-sandbox networks this driver
// creates, so they are distinguishable from unrelated networks on the same
// Docker host by name as well as by managedLabel. The sandbox ID is appended
// verbatim: per providersdk.NetworkIsolator.CreateSegment's contract, callers
// supply an ID already safe for use in provider-specific resource names.
const segmentNetworkPrefix = "boxy-sb-"

// CreateSegment creates a dedicated bridge network for one sandbox, on the
// caller-allocated cidr.
//
// The subnet is set explicitly rather than left to the daemon's auto-IPAM.
// Auto-IPAM picks a non-overlapping subnet on *this* host, which is exactly
// wrong for cross-host mesh peering: two daemons allocating from the same
// default pool in the same order hand out the same range, and WireGuard
// then drops the decrypted cross-host traffic as coming from a disallowed
// source address (#370). Only the caller, which sees every host, can pick a
// range that is unique across all of them.
//
// Giving up auto-IPAM means giving up its host-local collision avoidance,
// so the proposed range is checked against what this host already has
// first, and refused with a *providersdk.CIDRConflictError if it is
// unusable. The caller then proposes a different one.
//
// Idempotent per sandbox ID, as the interface requires: the deterministic
// network name is resolved first, and an existing network's ID is returned
// unchanged rather than issuing a second NetworkCreate. Without that check a
// retry would either fail on the duplicate name or leave a second, orphaned
// network behind that nothing ever tears down. cidr is ignored on that
// path -- the existing network's own subnet is authoritative, and
// renumbering a live network out from under its containers would be worse
// than honoring a stale proposal.
func (d *Driver) CreateSegment(ctx context.Context, sandboxID string, cidr string) (providersdk.SegmentRef, error) {
	name := segmentNetworkPrefix + sandboxID
	id, found, err := d.findSegmentNetwork(ctx, name)
	if err != nil {
		return "", err
	}
	if found {
		return providersdk.SegmentRef(id), nil
	}
	if err := d.checkCIDRAvailable(ctx, cidr); err != nil {
		return "", err
	}
	resp, err := d.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{managedLabel: managedLabelValue},
		IPAM: &network.IPAM{
			Driver: "default",
			Config: []network.IPAMConfig{{Subnet: cidr}},
		},
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

// checkCIDRAvailable reports whether cidr is free to use on this host,
// returning a *providersdk.CIDRConflictError naming the collision if not.
//
// Two sources, because they catch different things. Existing Docker
// networks' IPAM config covers everything the daemon manages, including
// `docker0` -- the collision actually observed in #370, where one host's
// default bridge occupied the other host's segment range. Host routes
// cover what the daemon knows nothing about: a VPN, the corporate LAN, an
// unrelated bridge. Neither alone is sufficient.
//
// A failure to enumerate either source is deliberately not fatal. Refusing
// to create any segment because `ip route` is unavailable would be a worse
// outcome than proceeding -- the caller-side global allocation is still in
// force, and this check is the local-collision backstop, not the primary
// guarantee.
func (d *Driver) checkCIDRAvailable(ctx context.Context, cidr string) error {
	if strings.TrimSpace(cidr) == "" {
		return fmt.Errorf("no segment CIDR supplied")
	}

	var inUse []string
	var sources []string

	if nets, err := d.cli.NetworkList(ctx, network.ListOptions{}); err == nil {
		for _, n := range nets {
			for _, c := range n.IPAM.Config {
				if c.Subnet != "" {
					inUse = append(inUse, c.Subnet)
					sources = append(sources, fmt.Sprintf("docker network %q (%s)", n.Name, c.Subnet))
				}
			}
		}
	}

	if out, err := d.runHost(ctx, "ip", "-4", "route", "show"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || !strings.Contains(fields[0], "/") {
				continue
			}
			inUse = append(inUse, fields[0])
			sources = append(sources, fmt.Sprintf("host route %s", strings.TrimSpace(line)))
		}
	}

	for i, candidate := range inUse {
		if segmentcidr.OverlapsAny(cidr, []string{candidate}) {
			return &providersdk.CIDRConflictError{RequestedCIDR: cidr, ConflictingWith: sources[i]}
		}
	}
	return nil
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
