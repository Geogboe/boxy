// Package builtins registers the built-in provider drivers with a Registry.
package builtins

import (
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/providers/devfactory"
	"github.com/Geogboe/boxy/pkg/providersdk/providers/docker"
)

// RegisterBuiltins registers all built-in provider types with reg.
func RegisterBuiltins(reg *providersdk.Registry) error {
	if err := reg.Register(devfactory.Registration()); err != nil {
		return err
	}
	if err := reg.Register(docker.Registration()); err != nil {
		return err
	}
	return nil
}
