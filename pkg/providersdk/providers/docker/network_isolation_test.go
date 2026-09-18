package docker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
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
		// No NetworkSettings at all: a container on no networks has nothing
		// to disconnect, so this case isolates the connect half. The mock's
		// networkDisconnect is left nil deliberately -- calling it would
		// panic, which is the assertion that no disconnect is attempted.
		containerInspect: func(_ context.Context, id string) (container.InspectResponse, error) {
			return runningInspect(id, "boxy-test"), nil
		},
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

// attachedInspect builds an InspectResponse for a running container wired to
// the given networks, keyed by network name the way the Docker daemon reports
// them, with each endpoint carrying the network's own ID.
func attachedInspect(containerID string, networksByName map[string]string) container.InspectResponse {
	resp := runningInspect(containerID, "boxy-test")
	networks := make(map[string]*network.EndpointSettings, len(networksByName))
	for name, id := range networksByName {
		networks[name] = &network.EndpointSettings{NetworkID: id}
	}
	resp.NetworkSettings = &container.NetworkSettings{Networks: networks}
	return resp
}

// TestDriver_AttachToSegment_DisconnectsContainerFromEveryOtherNetwork covers
// the isolation guarantee itself: Driver.Create leaves a new container on the
// daemon's default bridge, so connecting it to the segment without
// disconnecting the rest would leave it multi-homed and still reachable from
// every other container on the host.
func TestDriver_AttachToSegment_DisconnectsContainerFromEveryOtherNetwork(t *testing.T) {
	var connected []string
	var disconnected []string
	cli := &mockDockerClient{
		containerInspect: func(_ context.Context, id string) (container.InspectResponse, error) {
			return attachedInspect(id, map[string]string{
				"bridge": "net-bridge",
				"legacy": "net-legacy",
			}), nil
		},
		networkConnect: func(_ context.Context, networkID, containerID string, _ *network.EndpointSettings) error {
			connected = append(connected, networkID+"/"+containerID)
			return nil
		},
		networkDisconnect: func(_ context.Context, networkID, containerID string, _ bool) error {
			disconnected = append(disconnected, networkID+"/"+containerID)
			return nil
		},
	}
	d := &Driver{cli: cli}

	if err := d.AttachToSegment(context.Background(), "container-xyz", providersdk.SegmentRef("net-abc123")); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if len(connected) != 1 || connected[0] != "net-abc123/container-xyz" {
		t.Fatalf("NetworkConnect calls = %v, want exactly one to the segment", connected)
	}
	// Sorted, because AttachToSegment sorts before disconnecting so a
	// container on several networks produces a stable call sequence.
	want := []string{"bridge/container-xyz", "legacy/container-xyz"}
	if !slices.Equal(disconnected, want) {
		t.Fatalf("NetworkDisconnect calls = %v, want %v", disconnected, want)
	}
	for _, call := range disconnected {
		if strings.HasPrefix(call, "net-abc123/") {
			t.Fatalf("the target segment must never be disconnected, got %v", disconnected)
		}
	}
}

// TestDriver_AttachToSegment_SkipsConnectWhenAlreadyAttached covers the
// interface's idempotency contract: a retry after a partial attach must not
// re-issue the connect (the daemon rejects it as a conflict) but must still
// finish the disconnect sweep the first attempt did not get to.
func TestDriver_AttachToSegment_SkipsConnectWhenAlreadyAttached(t *testing.T) {
	var disconnected []string
	cli := &mockDockerClient{
		containerInspect: func(_ context.Context, id string) (container.InspectResponse, error) {
			// The segment is keyed by its network *name* while the
			// SegmentRef is the network *ID*, so recognizing it depends on
			// the endpoint's own NetworkID -- exactly the real daemon's
			// shape.
			return attachedInspect(id, map[string]string{
				"boxy-sb-sb-1": "net-abc123",
				"bridge":       "net-bridge",
			}), nil
		},
		networkConnect: func(_ context.Context, networkID, _ string, _ *network.EndpointSettings) error {
			t.Fatalf("NetworkConnect must not be called for an already-attached container, got %q", networkID)
			return nil
		},
		networkDisconnect: func(_ context.Context, networkID, _ string, _ bool) error {
			disconnected = append(disconnected, networkID)
			return nil
		},
	}
	d := &Driver{cli: cli}

	if err := d.AttachToSegment(context.Background(), "container-xyz", providersdk.SegmentRef("net-abc123")); err != nil {
		t.Fatalf("AttachToSegment on an already-attached container must be a no-op, got: %v", err)
	}
	if !slices.Equal(disconnected, []string{"bridge"}) {
		t.Fatalf("NetworkDisconnect calls = %v, want the disconnect sweep to still run for [bridge]", disconnected)
	}
}

// TestDriver_AttachToSegment_RecognizesTargetByNetworkName covers the
// defensive half of the target match: a SegmentRef that happens to equal the
// network's name (rather than its ID) must still be recognized as the target
// and left connected, not swept away as a foreign network.
func TestDriver_AttachToSegment_RecognizesTargetByNetworkName(t *testing.T) {
	cli := &mockDockerClient{
		containerInspect: func(_ context.Context, id string) (container.InspectResponse, error) {
			return attachedInspect(id, map[string]string{"boxy-sb-sb-1": "net-abc123"}), nil
		},
		networkConnect: func(_ context.Context, networkID, _ string, _ *network.EndpointSettings) error {
			t.Fatalf("NetworkConnect must not be called, got %q", networkID)
			return nil
		},
		networkDisconnect: func(_ context.Context, networkID, _ string, _ bool) error {
			t.Fatalf("NetworkDisconnect must not be called for the target segment, got %q", networkID)
			return nil
		},
	}
	d := &Driver{cli: cli}

	if err := d.AttachToSegment(context.Background(), "container-xyz", providersdk.SegmentRef("boxy-sb-sb-1")); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
}

// TestDriver_CreateSegment_RejectsNameCollisionWithUnmanagedNetwork covers
// findSegmentNetwork's managed-label check: the deterministic segment name
// is predictable, so an unrelated, unmanaged network happening to already
// own that name must not be silently adopted (and later torn down by
// DestroySegment, which does not belong to this driver).
func TestDriver_CreateSegment_RejectsNameCollisionWithUnmanagedNetwork(t *testing.T) {
	cli := &mockDockerClient{
		networkInspect: func(_ context.Context, networkID string, _ network.InspectOptions) (network.Inspect, error) {
			return network.Inspect{Name: networkID, ID: "net-unrelated"}, nil
		},
	}
	d := &Driver{cli: cli}

	if _, err := d.CreateSegment(context.Background(), "sb-1"); err == nil {
		t.Fatal("CreateSegment succeeded against an unmanaged name collision, want an error")
	}
}

// TestDriver_CreateSegment_IdempotentForSameSandboxID covers the interface's
// per-sandbox idempotency contract: a second call must return the first
// call's SegmentRef rather than creating a second network that nothing would
// ever tear down.
func TestDriver_CreateSegment_IdempotentForSameSandboxID(t *testing.T) {
	created := 0
	var inspected []string
	cli := &mockDockerClient{
		networkInspect: func(_ context.Context, networkID string, _ network.InspectOptions) (network.Inspect, error) {
			inspected = append(inspected, networkID)
			if created == 0 {
				return network.Inspect{}, notFoundError{msg: "network not found"}
			}
			return network.Inspect{Name: networkID, ID: "net-abc123", Labels: map[string]string{managedLabel: managedLabelValue}}, nil
		},
		networkCreate: func(_ context.Context, _ string, _ network.CreateOptions) (network.CreateResponse, error) {
			created++
			return network.CreateResponse{ID: "net-abc123"}, nil
		},
	}
	d := &Driver{cli: cli}

	first, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("first CreateSegment: %v", err)
	}
	second, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("second CreateSegment: %v", err)
	}
	if first != second {
		t.Fatalf("CreateSegment returned %q then %q, want the same SegmentRef both times", first, second)
	}
	if created != 1 {
		t.Fatalf("NetworkCreate called %d times, want exactly 1", created)
	}
	if !slices.Equal(inspected, []string{"boxy-sb-sb-1", "boxy-sb-sb-1"}) {
		t.Fatalf("NetworkInspect called with %v, want the deterministic segment name both times", inspected)
	}
}

// TestDriver_CreateSegment_ResolvesNetworkWhenCreateLosesARace covers the
// window between the existence check and NetworkCreate: a concurrent caller
// can create the network first, making this call's NetworkCreate fail on the
// duplicate name. The loser of that race must converge on the winner's
// network rather than surfacing the create error.
func TestDriver_CreateSegment_ResolvesNetworkWhenCreateLosesARace(t *testing.T) {
	inspects := 0
	cli := &mockDockerClient{
		networkInspect: func(_ context.Context, _ string, _ network.InspectOptions) (network.Inspect, error) {
			inspects++
			if inspects == 1 {
				return network.Inspect{}, notFoundError{msg: "network not found"}
			}
			return network.Inspect{Name: "boxy-sb-sb-1", ID: "net-winner", Labels: map[string]string{managedLabel: managedLabelValue}}, nil
		},
		networkCreate: func(_ context.Context, _ string, _ network.CreateOptions) (network.CreateResponse, error) {
			return network.CreateResponse{}, errors.New("network with name boxy-sb-sb-1 already exists")
		},
	}
	d := &Driver{cli: cli}

	ref, err := d.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment must not propagate a lost create race, got: %v", err)
	}
	if ref != "net-winner" {
		t.Fatalf("ref = %q, want the ID of the network the race winner created", ref)
	}
	if inspects != 2 {
		t.Fatalf("NetworkInspect called %d times, want a re-resolve after the failed create", inspects)
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
