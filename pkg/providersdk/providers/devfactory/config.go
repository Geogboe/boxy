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
}
