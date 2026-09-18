package providersdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Geogboe/boxy/pkg/resourcepack"
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

// GuestCredential is an opaque, driver-defined credential payload. The
// control plane relays it without interpreting Data; Kind is advisory for
// clients that know how to render or persist a particular credential kind.
type GuestCredential struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// GuestBootstrapCredential is the short-lived input used by a provider to
// authenticate to a freshly provisioned guest before rotating its password.
// It is never persisted in resource metadata.
type GuestBootstrapCredential struct {
	Username string
	Password string
}

// GuestBootstrapResolver supplies the server-owned bootstrap credential for a
// resource. Providers receive it as a callback so embedded and remote agents
// can use different transport implementations without changing driver APIs.
type GuestBootstrapResolver func(ctx context.Context, resourceID string) (GuestBootstrapCredential, error)

// AllocationResult separates safe properties, which may be persisted on a
// resource, from an ephemeral credential that must never enter resource
// metadata or the durable store.
type AllocationResult struct {
	Properties      map[string]any
	GuestCredential *GuestCredential
	AppliedPackages []resourcepack.AppliedPackage
}

// GuestPersonalizationResult is the typed result of allocation-time guest
// personalization. AccessDetails may be persisted; EphemeralCredential must
// remain process-local and be delivered to the caller exactly once.
type GuestPersonalizationResult struct {
	AccessDetails       GuestAccessDetails
	EphemeralCredential *GuestCredential
}

// GuestPersonalizationOptions carries phase-dependent behavior for a
// PersonalizeGuest call. It is a struct rather than a positional bool so a
// future phase-dependent step gets a new field instead of another
// positional argument.
type GuestPersonalizationOptions struct {
	// ApplyNetwork marks an allocation-time personalization — a sandbox is
	// actually claiming this resource — as opposed to an admission-time (pool
	// preheat) or promotion-time one, which must leave it false.
	//
	// It no longer describes in-guest IP assignment. A driver declaring
	// NetworkIsolator addresses a claimed guest from its sandbox's segment in
	// AttachToSegment instead, so there is no pool-declared address left for
	// PersonalizeGuest to apply (#224). What the flag gates now is anything a
	// driver may only do because an allocation is in flight — concretely, the
	// Hyper-V driver retains the credential it rotated the guest onto just
	// long enough for that allocation's AttachToSegment to authenticate with
	// it, and retains nothing for a preheated resource nothing is going to
	// attach.
	//
	// Credential rotation and verification themselves always run regardless
	// of this flag — PowerShell Direct/VMBus guest exec needs no network.
	// The underlying principle is unchanged from #358: a
	// preheated-but-unclaimed resource gets nothing it has no use for yet,
	// whether that is an address or a retained secret. See ADR-0012's 2026-09
	// change note and ADR-0021's 2026-09-14 entry.
	ApplyNetwork bool
}

// GuestPersonalizer is an optional provider capability for guest
// personalization with typed, safe returned access details. The same method
// is used at both admission time (pool preheat) and allocation time (a
// sandbox claiming the resource); opts.ApplyNetwork distinguishes them.
type GuestPersonalizer interface {
	PersonalizeGuest(ctx context.Context, id string, opts GuestPersonalizationOptions) (*GuestPersonalizationResult, error)
}
