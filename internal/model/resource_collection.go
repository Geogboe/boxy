package model

import (
	"fmt"

	"github.com/Geogboe/boxy/v2/pkg/resourcepool"
)

type resourceKey struct {
	Type    ResourceType
	Profile ResourceProfile
}

type keyedResource struct{ Resource }

func (r keyedResource) PoolKey() resourceKey {
	return resourceKey{Type: r.Type, Profile: r.Profile}
}

// ResourceCollection is a homogeneous container of resources.
type ResourceCollection struct {
	// ExpectedType is a constraint: all Resources must have Resource.Type == ExpectedType.
	ExpectedType ResourceType `json:"expected_type" yaml:"expected_type"`

	// ExpectedProfile is a constraint: all Resources must have Resource.Profile == ExpectedProfile.
	ExpectedProfile ResourceProfile `json:"expected_profile" yaml:"expected_profile"`

	Resources []Resource `json:"resources,omitempty" yaml:"resources,omitempty"`
}

// Add appends r if it satisfies the collection invariant.
func (c *ResourceCollection) Add(r Resource) error {
	if c == nil {
		return fmt.Errorf("resource collection is nil")
	}
	if c.ExpectedType == ResourceTypeUnknown {
		return fmt.Errorf("resource collection expected type is required")
	}
	if r.Type == ResourceTypeUnknown {
		return fmt.Errorf("resource type is required")
	}
	if c.ExpectedProfile == "" {
		return fmt.Errorf("resource collection expected profile is required")
	}
	if r.Profile == "" {
		return fmt.Errorf("resource profile is required")
	}

	p := resourcepool.Pool[resourceKey, keyedResource, struct{}]{
		Key:   resourceKey{Type: c.ExpectedType, Profile: c.ExpectedProfile},
		Items: wrapResources(c.Resources),
	}
	if err := p.Add(keyedResource{r}); err != nil {
		return err
	}
	c.Resources = unwrapResources(p.Items)
	return nil
}

// Validate checks that the collection is internally consistent.
func (c ResourceCollection) Validate() error {
	if c.ExpectedType == ResourceTypeUnknown {
		return fmt.Errorf("resource collection expected type is required")
	}
	if c.ExpectedProfile == "" {
		return fmt.Errorf("resource collection expected profile is required")
	}

	p := resourcepool.Pool[resourceKey, keyedResource, struct{}]{
		Key:   resourceKey{Type: c.ExpectedType, Profile: c.ExpectedProfile},
		Items: wrapResources(c.Resources),
	}
	return p.Validate()
}

func wrapResources(rs []Resource) []keyedResource {
	out := make([]keyedResource, 0, len(rs))
	for _, r := range rs {
		out = append(out, keyedResource{r})
	}
	return out
}

func unwrapResources(rs []keyedResource) []Resource {
	out := make([]Resource, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Resource)
	}
	return out
}
