package docker

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// CreateSegment creates a dedicated bridge network for one sandbox. No
// explicit IPAM/subnet is set -- the Docker daemon auto-assigns a
// non-overlapping subnet from its own default address pools, so unlike
// Hyper-V's implementation there is no collision-avoidance ledger to
// maintain here.
func (d *Driver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	name := "boxy-sb-" + sandboxID
	resp, err := d.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{managedLabel: managedLabelValue},
	})
	if err != nil {
		return "", fmt.Errorf("create docker network %q: %w", name, err)
	}
	return providersdk.SegmentRef(resp.ID), nil
}

// AttachToSegment connects an already-running container to the sandbox's
// network. No EndpointSettings are specified -- Docker assigns an address
// from the network's own auto-assigned subnet.
func (d *Driver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	if err := d.cli.NetworkConnect(ctx, string(ref), providerResourceID, nil); err != nil {
		return fmt.Errorf("connect container %q to segment %q: %w", providerResourceID, ref, err)
	}
	return nil
}

// DestroySegment removes the network created by CreateSegment. Idempotent
// for an already-gone network, matching Driver.Delete's contract.
func (d *Driver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	if err := d.cli.NetworkRemove(ctx, string(ref)); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove docker network %q: %w", ref, err)
	}
	return nil
}
