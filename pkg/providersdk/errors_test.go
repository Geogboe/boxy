package providersdk

import (
	"errors"
	"testing"
)

func TestCapacityError_ImplementsErrorTyper(t *testing.T) {
	var err error = &CapacityError{RequestedMemoryMB: 2048, AvailableMemoryMB: 512}
	var et ErrorTyper
	if !errors.As(err, &et) {
		t.Fatal("expected *CapacityError to satisfy ErrorTyper")
	}
	if et.ErrorType() != "capacity" {
		t.Errorf("ErrorType() = %q, want %q", et.ErrorType(), "capacity")
	}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
}

func TestOrphanedResourceError_ImplementsErrorTyper(t *testing.T) {
	var err error = &OrphanedResourceError{ID: "guid-1", CauseMessage: "remove-vm failed"}
	var et ErrorTyper
	if !errors.As(err, &et) {
		t.Fatal("expected *OrphanedResourceError to satisfy ErrorTyper")
	}
	if et.ErrorType() != "orphaned_resource" {
		t.Errorf("ErrorType() = %q, want %q", et.ErrorType(), "orphaned_resource")
	}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
}
