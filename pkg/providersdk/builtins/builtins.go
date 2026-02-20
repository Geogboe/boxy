// Package builtins registers the built-in provider drivers with a Registry.
package builtins

import (
	"github.com/Geogboe/boxy/v2/pkg/providersdk"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/providers/devfactory"
)

// RegisterBuiltins registers all built-in provider types with reg.
func RegisterBuiltins(reg *providersdk.Registry) error {
	reg.Register(devfactory.Registration())
	return nil
}
