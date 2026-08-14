// Package userconfig provides the shared per-user Boxy configuration root.
package userconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir returns the directory used for user-scoped Boxy configuration.
// XDG_CONFIG_HOME is honored on every platform so tests and managed
// environments can choose an explicit location.
func Dir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "boxy"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".config", "boxy"), nil
}
