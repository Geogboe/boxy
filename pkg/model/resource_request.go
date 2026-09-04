package model

import (
	"fmt"
	"strings"
)

// ResourceRequest expresses demand for one or more resources matching a (Type, Profile) key.
//
// This is intentionally small scaffolding:
// - It is not a full "spec" or "configuration".
// - It exists to make pooling/matching deterministic beyond coarse ResourceType.
//
// Examples:
//   - {Type: vm, Profile: "win-2022", Count: 3}
//   - {Type: container, Profile: "ubuntu-2204", Count: 1}
//   - {Type: share, Profile: "default", Count: 1}
type ResourceRequest struct {
	Type     ResourceType    `json:"type" yaml:"type"`
	Profile  ResourceProfile `json:"profile" yaml:"profile"`
	Count    int             `json:"count" yaml:"count"`
	Packages []string        `json:"packages,omitempty" yaml:"packages,omitempty"`
}

func (r ResourceRequest) Validate() error {
	if r.Type == "" || r.Type == ResourceTypeUnknown {
		return fmt.Errorf("request type is required")
	}
	if r.Profile == "" {
		return fmt.Errorf("request profile is required")
	}
	if r.Count <= 0 {
		return fmt.Errorf("request count must be > 0")
	}
	for i, rawRef := range r.Packages {
		ref := strings.TrimSpace(rawRef)
		name, version, ok := strings.Cut(ref, "@")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(version) == "" || strings.Contains(version, "@") {
			return fmt.Errorf("request package[%d] %q must use name@version", i, rawRef)
		}
	}
	return nil
}
