package sandbox

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

type fakeIsolatingAllocator struct {
	*fakeAllocator
	createRef    providersdk.SegmentRef
	createType   providersdk.Type
	createErr    error
	attachErr    error
	gotPool      model.Pool
	gotRes       model.Resource
	gotSbID      model.SandboxID
	gotAttachRef providersdk.SegmentRef
}

func (f *fakeIsolatingAllocator) CreateSegment(_ context.Context, pool model.Pool, res model.Resource, sandboxID model.SandboxID) (providersdk.SegmentRef, providersdk.Type, error) {
	f.gotPool, f.gotRes, f.gotSbID = pool, res, sandboxID
	return f.createRef, f.createType, f.createErr
}

func (f *fakeIsolatingAllocator) AttachToSegment(_ context.Context, _ model.Pool, _ model.Resource, ref providersdk.SegmentRef) error {
	f.gotAttachRef = ref
	return f.attachErr
}

func TestNetworkIsolatingAllocator_SatisfiedByTypeAssertion(t *testing.T) {
	var a SandboxAllocator = &fakeIsolatingAllocator{createRef: "seg-1", createType: "hyperv"}
	isolator, ok := a.(NetworkIsolatingAllocator)
	if !ok {
		t.Fatal("allocator implementing NetworkIsolatingAllocator's methods was not detected via type assertion")
	}
	ref, providerType, err := isolator.CreateSegment(context.Background(), model.Pool{Name: "pool-a"}, model.Resource{ID: "res-1"}, "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "seg-1" || providerType != "hyperv" {
		t.Fatalf("got (%q, %q), want (seg-1, hyperv)", ref, providerType)
	}
}
