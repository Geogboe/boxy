package devboxes

import "github.com/Geogboe/boxy/v2/pkg/providersdk"

// Registration returns the providersdk.Registration for the devboxes provider.
// Use this to register devboxes with a providersdk.Registry.
func Registration() providersdk.Registration {
	return providersdk.Registration{
		Type:        ProviderType,
		ConfigProto: func() any { return &Config{} },
		NewDriver: func(cfg any) (providersdk.Driver, error) {
			return New(cfg.(*Config)), nil
		},
	}
}
