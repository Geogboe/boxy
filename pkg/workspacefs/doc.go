// Package workspacefs contains helpers for creating and managing filesystem-backed
// scratch workspaces used by lightweight providers such as `pkg/provider/scratch`.
//
// # Overview
//
// Helpers in this package provide deterministic paths for workspace files,
// helper functions to provision directories and connect artifacts (connect
// scripts, env files), and health checks for presence and minimal free space.
//
// Typical helpers:
//
// - `Layout` - deterministic paths for a given resource ID.
// - `Provision` - create the directory and workspace layout.
// - `HealthCheck` - validate required files and minimum free space.
// - `Cleanup` - remove the workspace when the resource is destroyed.
//
// # API Notes
//
// The package offers a small surface area: `Layout`, `Provision`, `HealthCheck`,
// and `Cleanup` primitives. Callers are responsible for writing their own
// connect scripts and resource metadata files; workspacefs only provides the
// layout and helper functions.
//
// # Security
//
// This package is a convenience library for filesystem-backed scratch
// workspaces and does not implement any sandboxing. Do not rely on it for
// strong isolation guarantees.
package workspacefs
