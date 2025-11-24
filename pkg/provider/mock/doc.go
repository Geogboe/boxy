// Package mock provides a lightweight Provider implementation for tests and
// local development.
//
// Overview
//
// The mock provider simulates a backend provider without performing real
// operations. It allows higher-level Boxy components to be tested against a
// deterministic, controllable backend. The mock implementation simulates
// provisioning delays and can be configured to inject failures for test
// coverage.
//
// Requirements
//
// This package only requires the standard library and Boxy core types; there
// are no external runtime dependencies. It is safe to use on any platform.
//
// API Notes
//
// The mock provider implements the same Provider interface as the real
// providers. It is intended to be used by unit tests and in-memory integration
// tests where deterministic behavior is more valuable than fidelity.
//
// Security
//
// Because this package is a test utility, it intentionally uses weak,
// deterministic secrets. Avoid using the mock provider in production where
// credential hygiene is required.
package mock
