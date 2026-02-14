package model

import "fmt"

// ResourceCollection is a homogeneous container of resources.
type ResourceCollection struct {
	// ExpectedType is a constraint: all Resources must have Resource.Type == ExpectedType.
	ExpectedType ResourceType `json:"expected_type" yaml:"expected_type"`
	Resources []Resource   `json:"resources,omitempty" yaml:"resources,omitempty"`
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
	c.Resources = append(c.Resources, r)
	return nil
}

// Validate checks that the collection is internally consistent.
func (c ResourceCollection) Validate() error {
	if c.ExpectedType == ResourceTypeUnknown {
		return fmt.Errorf("resource collection expected type is required")
	}
	for i := range c.Resources {
		r := c.Resources[i]
		if r.Type == ResourceTypeUnknown {
			return fmt.Errorf("resource[%d] type is required", i)
		}
		if r.Type != c.ExpectedType {
			return fmt.Errorf("resource[%d] type mismatch: expected=%q got=%q", i, c.ExpectedType, r.Type)
		}
	}
	return nil
}
