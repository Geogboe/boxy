package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirectoryRegistry is a small local artifact registry. It is useful for
// development, air-gapped deployments, and tests; the Registry interface
// keeps callers independent from the backing store.
type DirectoryRegistry struct {
	Root string
}

func NewDirectoryRegistry(root string) (*DirectoryRegistry, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("artifact registry directory is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact registry directory: %w", err)
	}
	return &DirectoryRegistry{Root: root}, nil
}

func (r *DirectoryRegistry) Publish(ctx context.Context, value Artifact) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if value.Kind == "" || value.Ref.Name == "" || value.Ref.Version == "" {
		return fmt.Errorf("artifact kind, name, and version are required")
	}
	value.Ref.Kind = value.Kind
	path, err := r.artifactPath(value.Ref)
	if err != nil {
		return err
	}
	if existing, readErr := r.readArtifact(path); readErr == nil {
		if sameArtifact(existing, value) {
			return nil
		}
		return fmt.Errorf("artifact %q is immutable and already published with different content", value.Ref.String())
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	return r.writeJSON(path, cloneArtifact(value))
}

func (r *DirectoryRegistry) Resolve(ctx context.Context, ref Ref) (Artifact, error) {
	if err := contextErr(ctx); err != nil {
		return Artifact{}, err
	}
	path, err := r.artifactPath(ref)
	if err != nil {
		return Artifact{}, err
	}
	value, err := r.readArtifact(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Artifact{}, fmt.Errorf("artifact %q not found", ref.String())
		}
		return Artifact{}, err
	}
	if value.Kind != ref.Kind || value.Ref.Name != ref.Name || value.Ref.Version != ref.Version {
		return Artifact{}, fmt.Errorf("artifact %q has inconsistent identity", ref.String())
	}
	return value, nil
}

func (r *DirectoryRegistry) PutSource(ctx context.Context, source Source) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.Store) == "" || strings.TrimSpace(source.Path) == "" {
		return fmt.Errorf("source name, store, and path are required")
	}
	if err := ValidateDigest(source.Digest); err != nil {
		return fmt.Errorf("source %q: %w", source.Name, err)
	}
	path, err := r.sourcePath(source.Name)
	if err != nil {
		return err
	}
	if existing, readErr := r.readSource(path); readErr == nil {
		if sameSource(existing, source) {
			return nil
		}
		return fmt.Errorf("source %q is immutable and already registered with different metadata", source.Name)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	return r.writeJSON(path, cloneSource(source))
}

func (r *DirectoryRegistry) ResolveSource(ctx context.Context, name string) (Source, error) {
	if err := contextErr(ctx); err != nil {
		return Source{}, err
	}
	path, err := r.sourcePath(name)
	if err != nil {
		return Source{}, err
	}
	source, err := r.readSource(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Source{}, fmt.Errorf("source %q not found", name)
		}
		return Source{}, err
	}
	return source, nil
}

func (r *DirectoryRegistry) artifactPath(ref Ref) (string, error) {
	if r == nil || strings.TrimSpace(r.Root) == "" {
		return "", fmt.Errorf("artifact registry directory is required")
	}
	if ref.Kind == "" || !safeComponent(ref.Name) || !safeComponent(ref.Version) {
		return "", fmt.Errorf("artifact reference contains an unsafe path component")
	}
	return filepath.Join(r.Root, "artifacts", string(ref.Kind), ref.Name, ref.Version+".json"), nil
}

func (r *DirectoryRegistry) sourcePath(name string) (string, error) {
	if r == nil || strings.TrimSpace(r.Root) == "" {
		return "", fmt.Errorf("artifact registry directory is required")
	}
	if !safeComponent(name) {
		return "", fmt.Errorf("source name contains an unsafe path component")
	}
	return filepath.Join(r.Root, "sources", name+".json"), nil
}

func (r *DirectoryRegistry) readArtifact(path string) (Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, err
	}
	var value Artifact
	if err := json.Unmarshal(data, &value); err != nil {
		return Artifact{}, fmt.Errorf("decode artifact %q: %w", path, err)
	}
	return value, nil
}

func (r *DirectoryRegistry) readSource(path string) (Source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Source{}, err
	}
	var value Source
	if err := json.Unmarshal(data, &value); err != nil {
		return Source{}, fmt.Errorf("decode source %q: %w", path, err)
	}
	return value, nil
}

func (r *DirectoryRegistry) writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create artifact path: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode artifact: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".artifact-*.tmp")
	if err != nil {
		return fmt.Errorf("create artifact temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // best effort cleanup after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect artifact temporary file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close artifact temporary file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publish artifact: %w", err)
	}
	return nil
}

func sameArtifact(left, right Artifact) bool {
	return left.Kind == right.Kind && left.Ref == right.Ref && string(left.Manifest) == string(right.Manifest) && sameBlobs(left.Blobs, right.Blobs) && reflectStrings(left.Metadata, right.Metadata)
}

func sameSource(left, right Source) bool {
	return left.Name == right.Name && left.Store == right.Store && left.Path == right.Path && left.Digest == right.Digest && left.Format == right.Format && left.OS == right.OS && left.Provider == right.Provider && reflectStrings(left.Metadata, right.Metadata)
}

func safeComponent(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, `/\\:`)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func reflectStrings(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
