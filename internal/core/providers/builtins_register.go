package providers

import "github.com/Geogboe/boxy/v2/internal/core/providers/docker"

// RegisterBuiltins registers all compiled-in drivers.
//
// This is the single source of truth for "supported provider kinds":
// - runtime uses this to populate its Registry
// - schema generation uses this to derive the enum list
func RegisterBuiltins(r *Registry) error {
	if err := r.Register(docker.New()); err != nil {
		return err
	}
	return nil
}
