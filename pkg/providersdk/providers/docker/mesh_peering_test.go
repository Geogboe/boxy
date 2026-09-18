package docker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// errExitStatus2 stands in for the *exec.ExitError "ip route add" returns on
// a real host; only the combined-output text matters to the fix under test.
var errExitStatus2 = errors.New("exit status 2")

// fakeMeshInterface stands in for a real *meshnet.Interface in these unit
// tests -- no real OS TUN device, no actual WireGuard handshake. Peer keys
// here are not required to be valid Curve25519 hex, unlike the real
// meshnet.Interface.AddPeer, which is exactly why this fake exists.
type fakeMeshInterface struct {
	name   string
	pubKey string
	closed bool
}

func newFakeMeshInterface(ifName string) (meshInterface, error) {
	return &fakeMeshInterface{name: ifName, pubKey: "fake-pubkey-" + ifName}, nil
}

func (f *fakeMeshInterface) PublicKeyHex() string  { return f.pubKey }
func (f *fakeMeshInterface) Name() (string, error) { return f.name, nil }
func (f *fakeMeshInterface) AddPeer(_, _ string, _ []string) error {
	return nil
}
func (f *fakeMeshInterface) RemovePeer(_ string) error { return nil }
func (f *fakeMeshInterface) Close() error {
	f.closed = true
	return nil
}

func TestDriver_MeshIdentity_DiscoversSubnetAndInstallsRoute(t *testing.T) {
	var gotArgs []string
	cli := &mockDockerClient{
		networkInspect: func(_ context.Context, networkID string, _ network.InspectOptions) (network.Inspect, error) {
			if networkID != "net-abc123" {
				t.Fatalf("inspected %q, want net-abc123", networkID)
			}
			return network.Inspect{IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.30.0.0/24"}}}}, nil
		},
	}
	d := &Driver{cli: cli, meshEndpoint: "203.0.113.5:51820", newMeshInterface: newFakeMeshInterface}
	d.hostExec = func(_ context.Context, name string, args ...string) (string, error) {
		gotArgs = append(gotArgs, strings.Join(append([]string{name}, args...), " "))
		return "", nil
	}

	pub, endpoint, cidr, err := d.MeshIdentity(context.Background(), providersdk.SegmentRef("net-abc123"))
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub == "" {
		t.Fatal("expected a non-empty public key")
	}
	if endpoint != "203.0.113.5:51820" {
		t.Fatalf("endpoint = %q, want the configured mesh endpoint", endpoint)
	}
	if cidr != "172.30.0.0/24" {
		t.Fatalf("cidr = %q, want the discovered subnet", cidr)
	}
	found := false
	for _, call := range gotArgs {
		if strings.Contains(call, "ip route add 172.30.0.0/24") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an ip route add call for the discovered subnet, got calls: %v", gotArgs)
	}
	if err := d.closeMeshInterfaces(); err != nil {
		t.Fatalf("closeMeshInterfaces: %v", err)
	}
}

// TestDriver_MeshIdentity_TreatsRouteAlreadyExistsAsSuccess reproduces a real
// failure found running this end to end on a real Linux host: a Docker
// bridge network already owns a directly-connected route to its own subnet,
// so "ip route add <that subnet>" always fails with "RTNETLINK answers:
// File exists" -- the desired route already exists, so this must not fail
// MeshIdentity.
func TestDriver_MeshIdentity_TreatsRouteAlreadyExistsAsSuccess(t *testing.T) {
	cli := &mockDockerClient{
		networkInspect: func(context.Context, string, network.InspectOptions) (network.Inspect, error) {
			return network.Inspect{IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.30.0.0/24"}}}}, nil
		},
	}
	d := &Driver{cli: cli, meshEndpoint: "203.0.113.5:51820", newMeshInterface: newFakeMeshInterface}
	d.hostExec = func(context.Context, string, ...string) (string, error) {
		return "RTNETLINK answers: File exists\n", errExitStatus2
	}

	if _, _, _, err := d.MeshIdentity(context.Background(), providersdk.SegmentRef("net-abc123")); err != nil {
		t.Fatalf("MeshIdentity: %v, want success on an already-existing route", err)
	}
}

func TestDriver_MeshIdentity_FailsOnARealRouteError(t *testing.T) {
	cli := &mockDockerClient{
		networkInspect: func(context.Context, string, network.InspectOptions) (network.Inspect, error) {
			return network.Inspect{IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.30.0.0/24"}}}}, nil
		},
	}
	d := &Driver{cli: cli, meshEndpoint: "203.0.113.5:51820", newMeshInterface: newFakeMeshInterface}
	d.hostExec = func(context.Context, string, ...string) (string, error) {
		return "ip: command not found\n", errExitStatus2
	}

	if _, _, _, err := d.MeshIdentity(context.Background(), providersdk.SegmentRef("net-abc123")); err == nil {
		t.Fatal("expected MeshIdentity to fail on a real (non-\"File exists\") route error")
	}
}

// TestDriver_AddMeshPeer_RoutesThePeerSubnet reproduces a real gap found
// running this end to end on a real Linux host: AddPeer alone configures
// WireGuard's own crypto-routing (allowed_ips) but the kernel never hands
// cross-host traffic to the interface without an explicit route for the
// peer's subnet -- containers could not reach a peer's segment at all
// without it, even though MeshIdentity/AddMeshPeer both reported success.
func TestDriver_AddMeshPeer_RoutesThePeerSubnet(t *testing.T) {
	cli := &mockDockerClient{
		networkInspect: func(context.Context, string, network.InspectOptions) (network.Inspect, error) {
			return network.Inspect{IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.30.0.0/24"}}}}, nil
		},
	}
	var gotArgs []string
	d := &Driver{cli: cli, meshEndpoint: "203.0.113.5:51820", newMeshInterface: newFakeMeshInterface}
	d.hostExec = func(_ context.Context, name string, args ...string) (string, error) {
		gotArgs = append(gotArgs, strings.Join(append([]string{name}, args...), " "))
		return "", nil
	}
	if _, _, _, err := d.MeshIdentity(context.Background(), providersdk.SegmentRef("net-abc123")); err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}

	if err := d.AddMeshPeer(context.Background(), providersdk.SegmentRef("net-abc123"), "deadbeef", "203.0.113.9:51820", "10.250.0.8/29"); err != nil {
		t.Fatalf("AddMeshPeer: %v", err)
	}

	found := false
	for _, call := range gotArgs {
		if strings.Contains(call, "ip route add 10.250.0.8/29") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an ip route add call for the peer's subnet, got calls: %v", gotArgs)
	}
}

func TestDriver_AddMeshPeer_NoInterfaceYetErrors(t *testing.T) {
	d := &Driver{cli: &mockDockerClient{}}
	err := d.AddMeshPeer(context.Background(), providersdk.SegmentRef("net-abc123"), "deadbeef", "203.0.113.9:51820", "10.250.0.8/29")
	if err == nil {
		t.Fatal("expected an error -- AddMeshPeer before any MeshIdentity call for this segment is a caller error")
	}
}

func TestDriver_IsAMeshPeerer(t *testing.T) {
	var d providersdk.Driver = &Driver{cli: &mockDockerClient{}}
	if _, ok := d.(providersdk.MeshPeerer); !ok {
		t.Fatal("*docker.Driver must satisfy providersdk.MeshPeerer")
	}
}
