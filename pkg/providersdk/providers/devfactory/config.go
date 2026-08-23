// Package devfactory provides a reference implementation of the
// providersdk.Driver interface. It simulates resource lifecycle without
// requiring any real infrastructure, making it suitable for:
//
//   - End-to-end testing of the Boxy pipeline
//   - Reference implementation for provider authors
//   - Local development without Docker/Hyper-V/etc.
//
// Use type: devfactory in pool configuration to use this provider.
package devfactory

import (
	"encoding/json"
	"path/filepath"
	"time"
)

// Duration is a time.Duration that can be unmarshaled from a JSON string
// ("800ms", "1.5s") as well as from a JSON number (nanoseconds).
// This lets boxy.yaml authors write human-readable durations.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	// Try as a number (nanoseconds, the json encoding of time.Duration).
	var ns int64
	if err := json.Unmarshal(b, &ns); err == nil {
		*d = Duration(ns)
		return nil
	}
	// Try as a quoted string like "800ms" or "1.5s".
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(dur)
	return nil
}

// Config is the typed configuration for the devfactory driver.
// It is unmarshaled from the pool's config: YAML block.
type Config struct {
	// DataDir is the directory where devfactory.json is stored.
	// If empty, a temporary directory is created automatically.
	DataDir string `yaml:"data_dir" json:"data_dir"`

	// Profile determines what kind of resource this provider simulates.
	// Valid values: "container", "vm", "share". Default: "container".
	Profile Profile `yaml:"profile" json:"profile"`

	// Latency simulates provisioning delay. Resources take this long
	// to transition from creating to running. Zero uses the profile default.
	// Accepts human-readable strings: "800ms", "1.5s", "2s".
	Latency Duration `yaml:"latency" json:"latency"`

	// FailCreate causes Create to return an error when true.
	// Useful for testing error handling paths.
	FailCreate bool `yaml:"fail_create" json:"fail_create"`

	// FailUpdate causes Update to return an error when true.
	FailUpdate bool `yaml:"fail_update" json:"fail_update"`

	// FailDelete causes Delete to return an error when true.
	FailDelete bool `yaml:"fail_delete" json:"fail_delete"`

	// Labels are passed through to resource metadata.
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`

	// AvailableMemoryMB is the value Availability() reports as available memory.
	// Zero (the default) means unlimited — matches the zero-value-friendly
	// pattern the other fields here already use (e.g. FailCreate's zero
	// value means "don't fail").
	AvailableMemoryMB int64 `yaml:"available_memory_mb" json:"available_memory_mb"`

	// AvailableMemoryZero, when true, forces Availability() to report
	// exactly zero MB available, overriding AvailableMemoryMB.
	// AvailableMemoryMB's own zero value already means "unlimited" (see
	// above), so it cannot itself express "insufficient capacity" — the one
	// scenario a consumer would actually want to exercise
	// (CapacityError-equivalent handling) against a reference driver. This
	// flag adds that without changing AvailableMemoryMB's existing meaning
	// or any config's default behavior. See #181.
	AvailableMemoryZero bool `yaml:"available_memory_zero" json:"available_memory_zero"`

	// FailCreateAs, when non-empty, causes Create to fail with a specific
	// providersdk error type instead of a plain error — letting a consumer
	// exercise typed-error handling (ErrorTyper, RemoteAgent/gRPC
	// propagation, pool quarantine) against this reference driver without
	// real infrastructure. FailCreate (a plain error) takes precedence if
	// both are set. Supported values:
	//   - "capacity": returns *providersdk.CapacityError. RequestedMemoryMB
	//     is fixed (see simulatedMemoryRequestMB); AvailableMemoryMB reflects
	//     AvailableMemoryZero/AvailableMemoryMB when configured for a
	//     specific scenario, and otherwise defaults to a value below the
	//     request — asking for this knob at all already means "pretend
	//     there isn't room."
	//   - "orphaned_resource": returns *providersdk.OrphanedResourceError
	//     and leaves the resource's store record behind in "creating"
	//     state, simulating a create that partially succeeded and couldn't
	//     be confirmed torn down — so ResourceLister and quarantine/cleanup
	//     flows have something real to find and later Delete.
	FailCreateAs string `yaml:"fail_create_as" json:"fail_create_as"`
}

// ResolveRelativePaths implements providersdk.RelativePathResolver. A
// relative DataDir is resolved against baseDir (the directory of the boxy
// config file DataDir was loaded from) rather than being left to resolve
// against the process's own working directory — matching how Boxy's own
// .boxy/state.json resolves (see internal/cli/serve.go's serveStatePath).
// An empty or already-absolute DataDir, and an empty baseDir (no config
// file path known), are left untouched. See #181's design spec,
// "Persistence backend and DataDir resolution."
func (c *Config) ResolveRelativePaths(baseDir string) {
	if c.DataDir == "" || baseDir == "" || filepath.IsAbs(c.DataDir) {
		return
	}
	c.DataDir = filepath.Join(baseDir, c.DataDir)
}
