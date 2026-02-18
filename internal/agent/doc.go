// Package agent provides a small, reusable “work agent” runtime.
//
// An agent:
// - advertises identity/labels/capabilities
// - polls a control-plane for work
// - enforces a local policy gate
// - executes allowed actions locally via an injected Executor
// - reports results back to the control-plane
//
// Transport (gRPC, etc) is provided by subpackages or callers.
package agent
