package providersdk_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

type fakeMeshPeeringDriver struct {
	*fakeIsolatingDriver
	identityPublicKey string
	identityEndpoint  string
	identityCIDR      string
}

func (f *fakeMeshPeeringDriver) MeshIdentity(_ context.Context, _ providersdk.SegmentRef) (string, string, string, error) {
	return f.identityPublicKey, f.identityEndpoint, f.identityCIDR, nil
}
func (f *fakeMeshPeeringDriver) AddMeshPeer(_ context.Context, _ providersdk.SegmentRef, _, _, _ string) error {
	return nil
}
func (f *fakeMeshPeeringDriver) RemoveMeshPeer(_ context.Context, _ providersdk.SegmentRef, _ string) error {
	return nil
}

func TestMeshPeerer_DetectedByTypeAssertion(t *testing.T) {
	var d providersdk.Driver = &fakeMeshPeeringDriver{
		fakeIsolatingDriver: &fakeIsolatingDriver{},
		identityPublicKey:   "abc123",
		identityEndpoint:    "203.0.113.5:51820",
		identityCIDR:        "10.250.0.0/29",
	}
	peerer, ok := d.(providersdk.MeshPeerer)
	if !ok {
		t.Fatal("driver implementing MeshPeerer's methods was not detected via type assertion")
	}
	pub, endpoint, cidr, err := peerer.MeshIdentity(context.Background(), "boxy-sb-sb-1")
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub != "abc123" || endpoint != "203.0.113.5:51820" || cidr != "10.250.0.0/29" {
		t.Fatalf("got (%q, %q, %q)", pub, endpoint, cidr)
	}
}
