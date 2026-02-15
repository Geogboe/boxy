package model

import "fmt"

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
	if r.Type != c.ExpectedType {
		return fmt.Errorf("resource type mismatch: expected=%q got=%q", c.ExpectedType, r.Type)
	}
	if c.ExpectedProfile == "" {
		return fmt.Errorf("resource collection expected profile is required")
	}
	if r.Profile == "" {
		return fmt.Errorf("resource profile is required")
	}
	if r.Profile != c.ExpectedProfile {
		return fmt.Errorf("resource profile mismatch: expected=%q got=%q", c.ExpectedProfile, r.Profile)
	}
	c.Resources = append(c.Resources, r)
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
	for i := range c.Resources {
		r := c.Resources[i]
		if r.Type == ResourceTypeUnknown {
			return fmt.Errorf("resource[%d] type is required", i)
		}
		if r.Type != c.ExpectedType {
			return fmt.Errorf("resource[%d] type mismatch: expected=%q got=%q", i, c.ExpectedType, r.Type)
		}
		if r.Profile == "" {
			return fmt.Errorf("resource[%d] profile is required", i)
		}
		if r.Profile != c.ExpectedProfile {
			return fmt.Errorf("resource[%d] profile mismatch: expected=%q got=%q", i, c.ExpectedProfile, r.Profile)
		}
	}
	return nil
}
