// Package process implements a providersdk driver that executes local OS processes.
//
// This is intentionally simple scaffolding. "Isolation" here means the work runs
// in a separate OS process with a controllable environment and working directory.
// Stronger isolation (containers, VMs, sandboxes) can be layered later.
package process
