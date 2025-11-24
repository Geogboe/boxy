// Package shell is a scratch workspace provider that creates lightweight
// filesystem-backed workspaces for use as fast, local resources.
//
// Overview
//
// This provider uses the local filesystem to provision resource directories and
// emits artifacts a caller can use to connect to or run inside the workspace
// (connect scripts, env files, etc.). It does not provide isolation guarantees
// beyond the filesystem layout; it is a convenience provider for fast local
// runs and tests.
//
// Key behaviors
//
// - Provision: create workspace directories and resource metadata files
// - Allocate: write sandbox metadata, connect script, and env file
// - Health: check directory existence, metadata files, and free space
// - Destroy: remove the resource directory tree
//
// API Notes
//
// The provider implements the standard Provider interface and returns connection
// information that points to files (connect script) and directories created by
// the provider. It intentionally does not run processes or manage networking.
//
// Security
//
// This provider is not a security boundary and should not be used where
// untrusted workloads need strict isolation. The main value is speed and
// reproducibility for local testing and CI.
package shell
