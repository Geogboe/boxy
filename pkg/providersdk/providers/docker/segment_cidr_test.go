package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/network"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// TestCreateSegment_UsesProposedCIDRAsExplicitSubnet pins the reason
// auto-IPAM was given up: the daemon picking its own non-overlapping subnet
// is precisely what made two hosts collide (#370), so the caller-allocated
// range must reach NetworkCreate as an explicit IPAM subnet.
func TestCreateSegment_UsesProposedCIDRAsExplicitSubnet(t *testing.T) {
	var gotOpts network.CreateOptions
	cli := &mockDockerClient{
		networkCreate: func(_ context.Context, _ string, options network.CreateOptions) (network.CreateResponse, error) {
			gotOpts = options
			return network.CreateResponse{ID: "net-1"}, nil
		},
	}
	d := &Driver{cli: cli}

	if _, err := d.CreateSegment(context.Background(), "sb-1", "10.250.7.0/29"); err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if gotOpts.IPAM == nil || len(gotOpts.IPAM.Config) != 1 {
		t.Fatalf("IPAM = %+v, want exactly one explicit subnet config", gotOpts.IPAM)
	}
	if got, want := gotOpts.IPAM.Config[0].Subnet, "10.250.7.0/29"; got != want {
		t.Fatalf("subnet = %q, want the caller-proposed %q", got, want)
	}
}

// TestCreateSegment_RefusesCIDRCollidingWithAnExistingNetwork is the exact
// failure observed in #370: the other host's segment range is occupied here
// by a local bridge, so the WireGuard peer's traffic would be dropped as
// coming from a disallowed source. The driver must refuse, not create.
func TestCreateSegment_RefusesCIDRCollidingWithAnExistingNetwork(t *testing.T) {
	cli := &mockDockerClient{
		networkList: func(context.Context, network.ListOptions) ([]network.Summary, error) {
			return []network.Summary{{
				Name: "bridge",
				IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "10.250.0.0/16"}}},
			}}, nil
		},
		networkCreate: func(context.Context, string, network.CreateOptions) (network.CreateResponse, error) {
			t.Fatal("NetworkCreate must not run for a colliding CIDR")
			return network.CreateResponse{}, nil
		},
	}
	d := &Driver{cli: cli}

	_, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	var conflict *providersdk.CIDRConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a *CIDRConflictError so the caller re-proposes", err)
	}
	if conflict.RequestedCIDR != "10.250.0.0/29" {
		t.Fatalf("RequestedCIDR = %q, want the refused range", conflict.RequestedCIDR)
	}
	if !strings.Contains(conflict.ConflictingWith, "bridge") {
		t.Fatalf("ConflictingWith = %q, should name what it collided with", conflict.ConflictingWith)
	}
}

// TestCreateSegment_RefusesCIDRCollidingWithAHostRoute covers the class the
// Docker daemon itself cannot report: a VPN or corporate LAN route.
func TestCreateSegment_RefusesCIDRCollidingWithAHostRoute(t *testing.T) {
	cli := &mockDockerClient{
		networkCreate: func(context.Context, string, network.CreateOptions) (network.CreateResponse, error) {
			t.Fatal("NetworkCreate must not run for a colliding CIDR")
			return network.CreateResponse{}, nil
		},
	}
	d := &Driver{cli: cli, hostExec: func(_ context.Context, _ string, _ ...string) (string, error) {
		return "10.250.4.0/24 dev tun0 scope link\ndefault via 192.168.1.1 dev eth0\n", nil
	}}

	_, err := d.CreateSegment(context.Background(), "sb-1", "10.250.4.0/29")
	var conflict *providersdk.CIDRConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a *CIDRConflictError", err)
	}
	if !strings.Contains(conflict.ConflictingWith, "tun0") {
		t.Fatalf("ConflictingWith = %q, should name the conflicting host route", conflict.ConflictingWith)
	}
}

// TestCreateSegment_AcceptsNonCollidingCIDR guards the other direction:
// unrelated networks and routes must not cause spurious refusals, or the
// caller burns its whole proposal budget on a host that was fine.
func TestCreateSegment_AcceptsNonCollidingCIDR(t *testing.T) {
	cli := &mockDockerClient{
		networkList: func(context.Context, network.ListOptions) ([]network.Summary, error) {
			return []network.Summary{{
				Name: "bridge",
				IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.17.0.0/16"}}},
			}}, nil
		},
		networkCreate: func(context.Context, string, network.CreateOptions) (network.CreateResponse, error) {
			return network.CreateResponse{ID: "net-ok"}, nil
		},
	}
	d := &Driver{cli: cli, hostExec: func(_ context.Context, _ string, _ ...string) (string, error) {
		return "172.17.0.0/16 dev docker0\n192.168.1.0/24 dev eth0\n", nil
	}}

	ref, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29")
	if err != nil {
		t.Fatalf("CreateSegment refused a non-colliding CIDR: %v", err)
	}
	if ref != "net-ok" {
		t.Fatalf("ref = %q, want the created network's ID", ref)
	}
}

// TestCreateSegment_ProceedsWhenLocalProbesFail pins the conservative
// direction: the server-side global allocation is the primary guarantee and
// this probe is the backstop, so an unavailable `ip` binary or a failing
// NetworkList must not make segment creation impossible.
func TestCreateSegment_ProceedsWhenLocalProbesFail(t *testing.T) {
	cli := &mockDockerClient{
		networkList: func(context.Context, network.ListOptions) ([]network.Summary, error) {
			return nil, fmt.Errorf("daemon unreachable")
		},
		networkCreate: func(context.Context, string, network.CreateOptions) (network.CreateResponse, error) {
			return network.CreateResponse{ID: "net-ok"}, nil
		},
	}
	d := &Driver{cli: cli, hostExec: func(_ context.Context, _ string, _ ...string) (string, error) {
		return "", fmt.Errorf("ip: not found")
	}}

	if _, err := d.CreateSegment(context.Background(), "sb-1", "10.250.0.0/29"); err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
}
