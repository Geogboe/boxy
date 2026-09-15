package hyperv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestDriver_MeshIdentity_CreatesInterfaceLazilyAndReturnsCIDR(t *testing.T) {
	var script string
	d := mockDriver(func(_ context.Context, s string) (string, error) {
		script = s
		return "", nil
	})
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	d.meshEndpoint = "203.0.113.5:51820"
	alloc, err := d.segments().allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	ref := providersdk.SegmentRef(alloc.SwitchName)

	pub, endpoint, cidr, err := d.MeshIdentity(context.Background(), ref)
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub == "" {
		t.Fatal("expected a non-empty public key")
	}
	if endpoint != "203.0.113.5:51820" {
		t.Fatalf("endpoint = %q, want the configured mesh endpoint", endpoint)
	}
	if cidr != alloc.CIDR {
		t.Fatalf("cidr = %q, want %q (the segment's allocated CIDR)", cidr, alloc.CIDR)
	}
	if !strings.Contains(script, "New-NetRoute") {
		t.Fatalf("expected MeshIdentity's lazy interface creation to also install a route, got script: %s", script)
	}

	// Idempotent: a second call for the same segment must reuse the same
	// interface (same public key), not create a second one.
	pub2, _, _, err := d.MeshIdentity(context.Background(), ref)
	if err != nil {
		t.Fatalf("second MeshIdentity: %v", err)
	}
	if pub2 != pub {
		t.Fatalf("second call returned a different public key (%q vs %q) -- must reuse the existing interface", pub2, pub)
	}
	if err := d.closeMeshInterfaces(); err != nil {
		t.Fatalf("closeMeshInterfaces: %v", err)
	}
}

func TestDriver_MeshIdentity_MissingSegmentErrors(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	if _, _, _, err := d.MeshIdentity(context.Background(), providersdk.SegmentRef("never-created")); err == nil {
		t.Fatal("expected an error for a segment the ledger has never seen")
	}
}

func TestDriver_AddMeshPeer_ConfiguresThePeer(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	d.meshEndpoint = "203.0.113.5:51820"
	alloc, err := d.segments().allocate("sb-1")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	ref := providersdk.SegmentRef(alloc.SwitchName)
	if _, _, _, err := d.MeshIdentity(context.Background(), ref); err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}

	if err := d.AddMeshPeer(context.Background(), ref, "deadbeef", "203.0.113.9:51820", "10.250.0.8/29"); err != nil {
		t.Fatalf("AddMeshPeer: %v", err)
	}
	if err := d.closeMeshInterfaces(); err != nil {
		t.Fatalf("closeMeshInterfaces: %v", err)
	}
}

func TestDriver_AddMeshPeer_NoInterfaceYetErrors(t *testing.T) {
	d := mockDriver(func(context.Context, string) (string, error) { return "", nil })
	d.segmentLedgerPath = filepath.Join(t.TempDir(), "network-segments.json")
	err := d.AddMeshPeer(context.Background(), providersdk.SegmentRef("never-created"), "deadbeef", "203.0.113.9:51820", "10.250.0.8/29")
	if err == nil {
		t.Fatal("expected an error -- AddMeshPeer before any MeshIdentity call for this segment is a caller error")
	}
}

func TestDriver_IsAMeshPeerer(t *testing.T) {
	var d providersdk.Driver = mockDriver(func(context.Context, string) (string, error) { return "", nil })
	if _, ok := d.(providersdk.MeshPeerer); !ok {
		t.Fatal("*hyperv.Driver must satisfy providersdk.MeshPeerer")
	}
}
