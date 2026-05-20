package model

import (
	"strings"
	"testing"
)

func TestResourceCollectionAddEnforcesHomogeneousInventory(t *testing.T) {
	collection := ResourceCollection{
		ExpectedType:    ResourceTypeContainer,
		ExpectedProfile: "alpine",
	}
	res := Resource{ID: "res-1", Type: ResourceTypeContainer, Profile: "alpine", State: ResourceStateReady}

	if err := collection.Add(res); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(collection.Resources) != 1 || collection.Resources[0].ID != res.ID {
		t.Fatalf("resources = %+v, want res-1", collection.Resources)
	}

	err := collection.Add(Resource{ID: "res-2", Type: ResourceTypeVM, Profile: "alpine", State: ResourceStateReady})
	if err == nil {
		t.Fatal("Add mismatched type error = nil")
	}
	if len(collection.Resources) != 1 {
		t.Fatalf("resources len = %d, want original collection unchanged", len(collection.Resources))
	}
}

func TestResourceCollectionAddRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		c    *ResourceCollection
		res  Resource
		want string
	}{
		{
			name: "nil collection",
			c:    nil,
			res:  Resource{ID: "res-1", Type: ResourceTypeContainer, Profile: "alpine"},
			want: "resource collection is nil",
		},
		{
			name: "missing expected type",
			c:    &ResourceCollection{ExpectedProfile: "alpine"},
			res:  Resource{ID: "res-1", Type: ResourceTypeContainer, Profile: "alpine"},
			want: "pool key mismatch",
		},
		{
			name: "missing resource type",
			c:    &ResourceCollection{ExpectedType: ResourceTypeContainer, ExpectedProfile: "alpine"},
			res:  Resource{ID: "res-1", Profile: "alpine"},
			want: "pool key mismatch",
		},
		{
			name: "missing expected profile",
			c:    &ResourceCollection{ExpectedType: ResourceTypeContainer},
			res:  Resource{ID: "res-1", Type: ResourceTypeContainer, Profile: "alpine"},
			want: "expected profile",
		},
		{
			name: "missing resource profile",
			c:    &ResourceCollection{ExpectedType: ResourceTypeContainer, ExpectedProfile: "alpine"},
			res:  Resource{ID: "res-1", Type: ResourceTypeContainer},
			want: "resource profile",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.c.Add(tt.res)
			if err == nil {
				t.Fatalf("Add error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Add error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestResourceCollectionValidateDetectsBadInventory(t *testing.T) {
	valid := ResourceCollection{
		ExpectedType:    ResourceTypeContainer,
		ExpectedProfile: "alpine",
		Resources:       []Resource{{ID: "res-1", Type: ResourceTypeContainer, Profile: "alpine"}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate valid collection: %v", err)
	}

	mismatched := valid
	mismatched.Resources = []Resource{{ID: "res-1", Type: ResourceTypeVM, Profile: "alpine"}}
	if err := mismatched.Validate(); err == nil {
		t.Fatal("Validate mismatched collection error = nil")
	}

	missingProfile := valid
	missingProfile.Resources = []Resource{{ID: "res-1", Type: ResourceTypeContainer}}
	if err := missingProfile.Validate(); err == nil {
		t.Fatal("Validate missing-profile collection error = nil")
	}
}
