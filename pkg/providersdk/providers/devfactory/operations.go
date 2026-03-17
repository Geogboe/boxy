package devfactory

// ExecOp simulates executing a command on a resource.
// This is the devfactory equivalent of docker exec, PowerShell Direct, etc.
type ExecOp struct {
	Command []string
	Env     map[string]string
}

// SetStateOp changes the simulated state of a resource.
// Useful for testing state transitions (e.g. simulating a crash).
type SetStateOp struct {
	State string
}
