package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// cidrRecordingAllocator records every CIDR proposed to CreateSegment and
// can refuse a configured number of them, standing in for a host that
// already has those ranges occupied by something the server can't see.
type cidrRecordingAllocator struct {
	proposed  []string
	refuse    map[string]string // cidr -> what it supposedly collides with
	createErr error
}

func (f *cidrRecordingAllocator) Allocate(context.Context, model.Pool, model.Resource) (providersdk.AllocationResult, error) {
	return providersdk.AllocationResult{}, nil
}

func (f *cidrRecordingAllocator) CreateSegment(_ context.Context, _ model.Pool, res model.Resource, _ model.SandboxID, cidr string) (providersdk.SegmentRef, providersdk.Type, error) {
	f.proposed = append(f.proposed, cidr)
	if what, refused := f.refuse[cidr]; refused {
		return "", "", &providersdk.CIDRConflictError{RequestedCIDR: cidr, ConflictingWith: what}
	}
	if f.createErr != nil {
		return "", "", f.createErr
	}
	return providersdk.SegmentRef("seg-" + cidr), providersdk.Type(res.Provider.Name), nil
}

func (f *cidrRecordingAllocator) AttachToSegment(context.Context, model.Pool, model.Resource, providersdk.SegmentRef) error {
	return nil
}

func newCIDRTestManager(t *testing.T, alloc *cidrRecordingAllocator) (*Manager, store.Store) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemoryStore()

	res := model.Resource{
		ID: "res-1", OriginPool: "pool-a",
		Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault,
		State:    model.ResourceStateReady,
		Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"},
	}
	if err := st.PutResource(ctx, res); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "pool-a",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeVM,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{res},
		},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	return New(st, alloc), st
}

// TestCreateSegment_RecordsAllocatedCIDR is the base case: the server picks
// a range, hands it to the driver, and records it on the segment so later
// allocations can see it is taken.
func TestCreateSegment_RecordsAllocatedCIDR(t *testing.T) {
	alloc := &cidrRecordingAllocator{}
	m, _ := newCIDRTestManager(t, alloc)

	sb, err := m.CreateFromPool(context.Background(), "pool-a", 1, "sb", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if len(sb.NetworkSegments) != 1 {
		t.Fatalf("NetworkSegments = %+v, want one", sb.NetworkSegments)
	}
	if got, want := sb.NetworkSegments[0].CIDR, "10.250.0.0/29"; got != want {
		t.Fatalf("recorded CIDR = %q, want the first free block %q", got, want)
	}
	if len(alloc.proposed) != 1 || alloc.proposed[0] != "10.250.0.0/29" {
		t.Fatalf("proposed = %v, want exactly the first block", alloc.proposed)
	}
}

// TestCreateSegment_RetriesWhenHostRefusesTheRange covers the half of #370
// the server cannot see: the proposed range is globally free, but already
// occupied on that host by something else (docker0, a VPN). The host
// refuses, and the server must propose a different range rather than fail.
func TestCreateSegment_RetriesWhenHostRefusesTheRange(t *testing.T) {
	alloc := &cidrRecordingAllocator{
		refuse: map[string]string{
			"10.250.0.0/29": "docker network \"bridge\" (10.250.0.0/16)",
			"10.250.0.8/29": "host route 10.250.0.8/29 dev tun0",
		},
	}
	m, _ := newCIDRTestManager(t, alloc)

	sb, err := m.CreateFromPool(context.Background(), "pool-a", 1, "sb", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if got, want := sb.NetworkSegments[0].CIDR, "10.250.0.16/29"; got != want {
		t.Fatalf("settled on CIDR %q, want %q (the first the host did not refuse)", got, want)
	}
	want := []string{"10.250.0.0/29", "10.250.0.8/29", "10.250.0.16/29"}
	if len(alloc.proposed) != len(want) {
		t.Fatalf("proposed = %v, want each refused range tried once then the next: %v", alloc.proposed, want)
	}
	for i, w := range want {
		if alloc.proposed[i] != w {
			t.Fatalf("proposal %d = %q, want %q (a refused range must not be re-proposed)", i, alloc.proposed[i], w)
		}
	}
}

// TestCreateSegment_GivesUpAfterTooManyRefusals pins the bound: a host that
// refuses everything must produce a clear failure rather than walking all
// 8192 blocks one round trip at a time.
func TestCreateSegment_GivesUpAfterTooManyRefusals(t *testing.T) {
	refuse := map[string]string{}
	for _, c := range []string{
		"10.250.0.0/29", "10.250.0.8/29", "10.250.0.16/29", "10.250.0.24/29",
		"10.250.0.32/29", "10.250.0.40/29", "10.250.0.48/29", "10.250.0.56/29",
		"10.250.0.64/29",
	} {
		refuse[c] = "everything is taken on this host"
	}
	alloc := &cidrRecordingAllocator{refuse: refuse}
	m, _ := newCIDRTestManager(t, alloc)

	_, err := m.CreateFromPool(context.Background(), "pool-a", 1, "sb", model.SandboxPolicies{})
	if err == nil {
		t.Fatal("expected failure when the host refuses every proposal")
	}
	if !strings.Contains(err.Error(), "no usable segment CIDR") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alloc.proposed) != maxCIDRProposals {
		t.Fatalf("made %d proposals, want the %d cap", len(alloc.proposed), maxCIDRProposals)
	}
}

// TestAllocatedSegmentCIDRs_SkipsRangesAlreadyTakenByOtherSandboxes is the
// cross-host half of #370: a range recorded on any sandbox is off limits,
// because the host holding it may be meshed with the host asking next.
func TestAllocatedSegmentCIDRs_SkipsRangesAlreadyTakenByOtherSandboxes(t *testing.T) {
	alloc := &cidrRecordingAllocator{}
	m, st := newCIDRTestManager(t, alloc)

	existing := model.Sandbox{
		ID:     "sb-existing",
		Status: model.SandboxStatusReady,
		NetworkSegments: []model.NetworkSegment{
			{AgentID: "agent-9", ProviderType: "hyperv", Ref: "sw", CIDR: "10.250.0.0/29"},
		},
	}
	if err := st.CreateSandbox(context.Background(), existing); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	sb, err := m.CreateFromPool(context.Background(), "pool-a", 1, "sb", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if got := sb.NetworkSegments[0].CIDR; got == "10.250.0.0/29" {
		t.Fatal("reused a range another sandbox already holds -- cross-host mesh would drop this traffic")
	}
	if got, want := sb.NetworkSegments[0].CIDR, "10.250.0.8/29"; got != want {
		t.Fatalf("allocated %q, want the next free block %q", got, want)
	}
}
