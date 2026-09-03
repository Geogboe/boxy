package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SourceDescriptor is the provider-neutral handoff for a source's bytes.
// Exactly one of Path or URL is set. URL is intentionally short-lived and is
// never suitable for persistence; providers use it only while provisioning.
type SourceDescriptor struct {
	URL       string            `json:"url,omitempty" yaml:"url,omitempty"`
	Path      string            `json:"path,omitempty" yaml:"path,omitempty"`
	Digest    string            `json:"digest" yaml:"digest"`
	Format    string            `json:"format,omitempty" yaml:"format,omitempty"`
	OS        string            `json:"os,omitempty" yaml:"os,omitempty"`
	Provider  string            `json:"provider,omitempty" yaml:"provider,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	ExpiresAt time.Time         `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
}

// Validate verifies that a provider can materialize this descriptor without
// guessing which transport to use. A URL is restricted to HTTP(S), including
// signed URLs from S3-compatible stores.
func (d SourceDescriptor) Validate() error {
	hasURL := strings.TrimSpace(d.URL) != ""
	hasPath := strings.TrimSpace(d.Path) != ""
	if hasURL == hasPath {
		return fmt.Errorf("source descriptor must contain exactly one of url or path")
	}
	if err := ValidateDigest(d.Digest); err != nil {
		return err
	}
	if hasURL {
		u, err := url.Parse(strings.TrimSpace(d.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("source descriptor url must be an absolute http(s) URL")
		}
		if !d.ExpiresAt.IsZero() && !time.Now().Before(d.ExpiresAt) {
			return fmt.Errorf("source descriptor url has expired")
		}
	}
	return nil
}

// PullSource materializes a descriptor into destination and verifies the
// declared digest while bytes are written. It uses a same-directory temporary
// file so a cancelled or corrupt download can never replace a usable source.
func PullSource(ctx context.Context, descriptor SourceDescriptor, destination string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := descriptor.Validate(); err != nil {
		return err
	}
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return fmt.Errorf("source destination is required")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create source destination: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".boxy-source-*.tmp")
	if err != nil {
		return fmt.Errorf("create source temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect source temporary file: %w", err)
	}

	var input io.ReadCloser
	if descriptor.Path != "" {
		input, err = os.Open(descriptor.Path)
		if err != nil {
			_ = tmp.Close()
			return fmt.Errorf("open source %q: %w", descriptor.Path, err)
		}
	} else {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, descriptor.URL, nil)
		if requestErr != nil {
			_ = tmp.Close()
			return fmt.Errorf("create source download request: %w", requestErr)
		}
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			_ = tmp.Close()
			return fmt.Errorf("download source: %w", requestErr)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			_ = resp.Body.Close()
			_ = tmp.Close()
			return fmt.Errorf("download source: unexpected HTTP status %s", resp.Status)
		}
		input = resp.Body
	}
	defer func() { _ = input.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), input); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("read source: %w", err)
	}
	if err := input.Close(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("close source: %w", err)
	}
	if got := "sha256:" + hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, strings.TrimSpace(descriptor.Digest)) {
		_ = tmp.Close()
		return fmt.Errorf("source digest mismatch: got %s, want %s", got, descriptor.Digest)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close source temporary file: %w", err)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return fmt.Errorf("materialize source: %w", err)
	}
	return nil
}
