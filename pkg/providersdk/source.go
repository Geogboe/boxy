package providersdk

import (
	"context"

	"github.com/Geogboe/boxy/pkg/artifact"
)

// SourceDescriptor is the provider-neutral source handoff. The alias keeps
// provider packages independent from artifact registry implementations while
// allowing the registry and provider to share one wire shape.
type SourceDescriptor = artifact.SourceDescriptor

// PullSource downloads or copies a source and verifies its declared digest.
// Providers should call this at the point they need the bytes, not in the
// control plane or agent orchestration layer.
func PullSource(ctx context.Context, descriptor SourceDescriptor, destination string) error {
	return artifact.PullSource(ctx, descriptor, destination)
}
