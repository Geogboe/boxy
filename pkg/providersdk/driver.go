package providersdk

import "context"

// Driver is the minimal interface a provider-kind implementation exposes.
//
// Keep this small. Prefer optional capability interfaces for additional behavior.
type Driver interface {
	Type() Type
	ValidateConfig(ctx context.Context, inst Instance) error
}
