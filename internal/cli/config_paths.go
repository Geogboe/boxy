package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

func findConfigPathInDir(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("dir is required")
	}
	candidates := []string{
		filepath.Join(dir, "boxy.yaml"),
		filepath.Join(dir, "boxy.yml"),
	}
	for _, p := range candidates {
		_, err := os.Stat(p)
		if err == nil {
			return p, nil
		}
		if os.IsNotExist(err) {
			continue
		}
		return "", fmt.Errorf("stat %q: %w", p, err)
	}
	return "", nil
}
