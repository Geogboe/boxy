package builtins

import (
	"github.com/Geogboe/boxy/v2/pkg/providersdk"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/drivers/docker"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/drivers/hyperv"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/drivers/process"
)

// RegisterBuiltins registers all compiled-in providersdk drivers.
func RegisterBuiltins(r *providersdk.Registry) error {
	if err := r.Register(docker.New()); err != nil {
		return err
	}
	if err := r.Register(hyperv.New()); err != nil {
		return err
	}
	if err := r.Register(process.New()); err != nil {
		return err
	}
	return nil
}
