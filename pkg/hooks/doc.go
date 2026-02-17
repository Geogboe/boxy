// Package hooks provides a small, testable runner for executing lifecycle hooks
// against a resource via an abstract Executor.
//
// The goal is to keep hook orchestration (timeouts, retries, ordering) separate
// from provider-specific execution details (docker exec, WinRM, etc).
package hooks
