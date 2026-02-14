package docker

import (
	"fmt"
	"strings"
)

func (c Config) Validate() error {
	// Require exactly one of host or context for determinism.
	if c.Host == "" && c.Context == "" {
		return fmt.Errorf("either host or context is required")
	}
	if c.Host != "" && c.Context != "" {
		return fmt.Errorf("only one of host or context may be set")
	}

	if c.Namespace == "" {
		// Default is intentionally handled in validation so callers can rely on it.
		// This does not mutate the original provider config blob.
		c.Namespace = "boxy"
	}

	if c.Host != "" {
		if !(strings.HasPrefix(c.Host, "unix://") || strings.HasPrefix(c.Host, "tcp://")) {
			return fmt.Errorf("host must start with unix:// or tcp://")
		}
	}

	if c.TLS.Enabled {
		// TLS only makes sense for remote TCP endpoints.
		if c.Host == "" || !strings.HasPrefix(c.Host, "tcp://") {
			return fmt.Errorf("tls.enabled requires a tcp:// host")
		}
	}

	return nil
}

