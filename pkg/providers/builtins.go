package providers

import (
	"github.com/Geogboe/boxy/v2/pkg/providers/docker"
	"github.com/Geogboe/boxy/v2/pkg/providers/hyperv"
	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

// RegisterBuiltins registers all compiled-in provider drivers.
//
// This is the single source of truth for "supported provider kinds" at the SDK
// layer. Boxy runtime wiring may choose to use or ignore this helper.
func RegisterBuiltins(r *providersdk.Registry) error {
	if err := r.Register(docker.New()); err != nil {
		return err
	}
	if err := r.Register(hyperv.New()); err != nil {
		return err
	}
	return nil
}
