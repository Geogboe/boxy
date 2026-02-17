// Package providersdk defines small interfaces and types for interacting with
// provider instances (Docker hosts, Hyper-V hosts, etc).
//
// This is intended as a reusable SDK surface:
//   - "provider instance" is configuration (where/how to connect)
//   - "driver" is code for a provider kind (docker, hyperv, ...)
//   - optional capability interfaces describe what a kind can do (VMs, containers,
//     guest operations, exec, resizing, ...).
//
// Higher-level orchestration (pooling, caching, policy reconciliation) belongs
// elsewhere.
package providersdk
