package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestDriver_CreateSegment_CreatesLabeledBridgeNetwork(t *testing.T) {
	var gotName string
	var gotOpts network.CreateOptions
	cli := &mockDockerClient{
		networkCreate: func(_ context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
			gotName = name
			gotOpts = options
			return network.CreateResponse{ID: "net-abc123"}, nil
		},
	}
	d := &Driver{cli: cli}

	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "net-abc123" {
		t.Fatalf("ref = %q, want the network ID returned by NetworkCreate", ref)
	}
	if gotName != "boxy-sb-sb-1" {
		t.Fatalf("network name = %q, want %q", gotName, "boxy-sb-sb-1")
	}
	if gotOpts.Driver != "bridge" {
		t.Fatalf("Driver = %q, want %q", gotOpts.Driver, "bridge")
	}
	if gotOpts.Labels[managedLabel] != managedLabelValue {
		t.Fatalf("network missing managed label: %+v", gotOpts.Labels)
	}
}

func TestDriver_AttachToSegment_ConnectsContainerToNetwork(t *testing.T) {
	var gotNetworkID, gotContainerID string
	cli := &mockDockerClient{
		networkConnect: func(_ context.Context, networkID, containerID string, _ *network.EndpointSettings) error {
			gotNetworkID, gotContainerID = networkID, containerID
			return nil
		},
	}
	d := &Driver{cli: cli}

	err := d.AttachToSegment(context.Background(), "container-xyz", providersdk.SegmentRef("net-abc123"))
	if err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if gotNetworkID != "net-abc123" || gotContainerID != "container-xyz" {
		t.Fatalf("NetworkConnect called with (%q, %q)", gotNetworkID, gotContainerID)
	}
}

func TestDriver_DestroySegment_RemovesNetwork(t *testing.T) {
	var gotID string
	cli := &mockDockerClient{
		networkRemove: func(_ context.Context, networkID string) error {
			gotID = networkID
			return nil
		},
	}
	d := &Driver{cli: cli}

	if err := d.DestroySegment(context.Background(), providersdk.SegmentRef("net-abc123")); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
	if gotID != "net-abc123" {
		t.Fatalf("NetworkRemove called with %q, want %q", gotID, "net-abc123")
	}
}

func TestDriver_DestroySegment_IdempotentWhenAlreadyGone(t *testing.T) {
	cli := &mockDockerClient{
		networkRemove: func(_ context.Context, _ string) error {
			return notFoundError{msg: "network not found"}
		},
	}
	d := &Driver{cli: cli}

	if err := d.DestroySegment(context.Background(), providersdk.SegmentRef("net-gone")); err != nil {
		t.Fatalf("DestroySegment on an already-gone network must be a no-op, got: %v", err)
	}
}

func TestDriver_IsANetworkIsolator(t *testing.T) {
	var d providersdk.Driver = &Driver{cli: &mockDockerClient{}}
	if _, ok := d.(providersdk.NetworkIsolator); !ok {
		t.Fatal("*docker.Driver must satisfy providersdk.NetworkIsolator")
	}
}
