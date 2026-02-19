// Package devbox provides an in-memory reference implementation of the
// providersdk.Driver interface. It simulates resource lifecycle without
// requiring any real infrastructure, making it suitable for:
//
//   - End-to-end testing of the Boxy pipeline
//   - Reference implementation for provider authors
//   - Local development without Docker/Hyper-V/etc.
//
// Use type: devbox in pool configuration to use this provider.
package devbox

import "time"

// Config is the typed configuration for the devbox driver.
// It is unmarshaled from the pool's config: YAML block.
type Config struct {
	// Latency simulates provisioning delay. Resources take this long
	// to transition from creating to running. Zero means instant.
	Latency time.Duration `yaml:"latency" json:"latency"`

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
