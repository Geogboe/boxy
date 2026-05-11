package providersdk

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// SecretRef is an opaque provider-managed lookup handle for a secret.
//
// The initial built-in resolver supports env:NAME references so providers can
// avoid persisting raw bootstrap secrets in resource metadata.
type SecretRef string

// ResolveSecretRef resolves a secret reference to its current secret value.
//
// Supported forms:
//   - env:NAME
func ResolveSecretRef(_ context.Context, ref SecretRef) (string, error) {
	raw := strings.TrimSpace(string(ref))
	if raw == "" {
		return "", fmt.Errorf("secret ref is required")
	}

	kind, name, ok := strings.Cut(raw, ":")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return "", fmt.Errorf("invalid secret ref %q", raw)
	}

	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "env":
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			return "", fmt.Errorf("secret ref %q: environment variable %q is not set", raw, name)
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported secret ref kind %q", kind)
	}
}

// GuestAccessDetails is the safe provider-returned connection metadata for a
// personalized guest. These values may be persisted and surfaced to the CLI.
type GuestAccessDetails struct {
	Properties map[string]string
}

// ToProperties converts safe string properties into the model.Resource.Properties
// shape used by the rest of Boxy.
func (d GuestAccessDetails) ToProperties() map[string]any {
	if len(d.Properties) == 0 {
		return nil
	}

	props := make(map[string]any, len(d.Properties))
	for k, v := range d.Properties {
		props[k] = v
	}
	return props
}

// GuestPersonalizationResult is the typed result of allocation-time guest
// personalization. It intentionally exposes only safe access details.
type GuestPersonalizationResult struct {
	AccessDetails GuestAccessDetails
}

// GuestPersonalizer is an optional provider capability for allocation-time
// guest personalization with typed, safe returned access details.
type GuestPersonalizer interface {
	PersonalizeGuest(ctx context.Context, id string) (*GuestPersonalizationResult, error)
}
