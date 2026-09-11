package providersdk_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// fakeIsolatingDriver satisfies providersdk.Driver minimally (only what the
// type assertion below needs to compile) plus providersdk.NetworkIsolator,
// to prove a driver can be detected as a NetworkIsolator by type assertion —
// the same pattern every other optional capability in this package uses.
type fakeIsolatingDriver struct{ createCalls int }

func (f *fakeIsolatingDriver) Type() providersdk.Type { return "fake" }
func (f *fakeIsolatingDriver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	return nil, nil
}
func (f *fakeIsolatingDriver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	return nil, nil
}
func (f *fakeIsolatingDriver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	return nil, nil
}
func (f *fakeIsolatingDriver) Delete(ctx context.Context, id string) error { return nil }
func (f *fakeIsolatingDriver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	return nil, nil
}

func (f *fakeIsolatingDriver) CreateSegment(ctx context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	f.createCalls++
	return providersdk.SegmentRef("seg-" + sandboxID), nil
}
func (f *fakeIsolatingDriver) AttachToSegment(ctx context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	return nil
}
func (f *fakeIsolatingDriver) DestroySegment(ctx context.Context, ref providersdk.SegmentRef) error {
	return nil
}

func TestNetworkIsolator_DetectedByTypeAssertion(t *testing.T) {
	var d providersdk.Driver = &fakeIsolatingDriver{}
	isolator, ok := d.(providersdk.NetworkIsolator)
	if !ok {
		t.Fatal("driver implementing NetworkIsolator's methods was not detected via type assertion")
	}
	ref, err := isolator.CreateSegment(context.Background(), "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "seg-sb-1" {
		t.Fatalf("ref = %q, want %q", ref, "seg-sb-1")
	}
}

func TestNetworkIsolator_NonImplementingDriverFailsAssertion(t *testing.T) {
	var d providersdk.Driver = &nonIsolatingDriver{}
	if _, ok := d.(providersdk.NetworkIsolator); ok {
		t.Fatal("a driver with no NetworkIsolator methods must not satisfy the interface")
	}
}

type nonIsolatingDriver struct{}

func (nonIsolatingDriver) Type() providersdk.Type { return "fake" }
func (nonIsolatingDriver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	return nil, nil
}
func (nonIsolatingDriver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	return nil, nil
}
func (nonIsolatingDriver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	return nil, nil
}
func (nonIsolatingDriver) Delete(ctx context.Context, id string) error { return nil }
func (nonIsolatingDriver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	return nil, nil
}
