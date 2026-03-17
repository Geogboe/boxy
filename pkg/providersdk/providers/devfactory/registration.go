package devfactory

import "github.com/Geogboe/boxy/pkg/providersdk"

// Registration returns the providersdk.Registration for the devfactory provider.
// Use this to register devfactory with a providersdk.Registry.
func Registration() providersdk.Registration {
	return providersdk.Registration{
		Type:        ProviderType,
		ConfigProto: func() any { return &Config{} },
		NewDriver: func(cfg any) (providersdk.Driver, error) {
			return New(cfg.(*Config)), nil
		},
	}
}
