package workspacefs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Paths holds important locations for a resource.
type Paths struct {
	RootDir       string
	WorkspaceDir  string
	ConnectScript string
	EnvFile       string
}

// Layout returns the deterministic layout for a resource under baseDir.
func Layout(baseDir, resourceID string) Paths {
	root := filepath.Join(baseDir, resourceID)
	return Paths{
		RootDir:       root,
		WorkspaceDir:  filepath.Join(root, "workspace"),
		ConnectScript: filepath.Join(root, "connect.sh"),
		EnvFile:       filepath.Join(root, ".envrc"),
	}
}

// PathsFromRoot builds Paths from an existing root directory path.
// Use this when you have resource.ProviderID (the full root path).
func PathsFromRoot(rootDir string) Paths {
	return Paths{
		RootDir:       rootDir,
		WorkspaceDir:  filepath.Join(rootDir, "workspace"),
		ConnectScript: filepath.Join(rootDir, "connect.sh"),
		EnvFile:       filepath.Join(rootDir, ".envrc"),
	}
}

// Provision creates directories for the workspace.
func Provision(baseDir, resourceID string) (Paths, error) {
	p := Layout(baseDir, resourceID)

	if err := os.MkdirAll(p.WorkspaceDir, 0o755); err != nil {
		return p, fmt.Errorf("create workspace dir: %w", err)
	}

	return p, nil
}

// HealthCheck validates presence and readability.
func HealthCheck(p Paths, requiredFiles []string, minFreeBytes uint64) error {
	if _, err := os.Stat(p.RootDir); err != nil {
		return fmt.Errorf("root missing: %w", err)
	}
	if _, err := os.Stat(p.WorkspaceDir); err != nil {
		return fmt.Errorf("workspace missing: %w", err)
	}

	for _, f := range requiredFiles {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("required file missing: %s: %w", f, err)
		}
	}

	if minFreeBytes > 0 {
		statfs := newStatfs()
		if err := statfs.Statfs(p.RootDir); err == nil {
			free := statfs.FreeBytes()
			if free < minFreeBytes {
				return fmt.Errorf("insufficient free space: have %d, need %d", free, minFreeBytes)
			}
		}
	}
	return nil
}

// Cleanup removes the entire resource directory tree.
func Cleanup(p Paths) error {
	return os.RemoveAll(p.RootDir)
}

// WriteJSONFile writes JSON to the given path with mode 0644.
func WriteJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// NewStatForTest exposes statfs for tests in other packages.
func NewStatForTest() syscallStatfs {
	return newStatfs()
}

// Minimal statfs abstraction to keep imports small and portable.
type syscallStatfs interface {
	Statfs(path string) error
	FreeBytes() uint64
}
