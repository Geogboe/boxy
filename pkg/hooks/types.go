package hooks

import "time"

// Event is the lifecycle moment a hook runs for.
type Event string

const (
	EventCreate  Event = "create"
	EventDestroy Event = "destroy"
)

// Target identifies where a hook should run.
//
// Ref is an opaque identifier interpreted by the Executor (e.g. container ID,
// VM name/ID). Kind lets one Executor support multiple target types.
type Target struct {
	Kind     string
	Ref      string
	Metadata map[string]string
}

// Hook is a single action to execute.
//
// Use Command for argv-style execution. Use ShellCommand for a shell string to
// be wrapped by Defaults.Shell (or /bin/sh -lc by default).
type Hook struct {
	Name string

	Command      []string
	ShellCommand string

	Timeout time.Duration

	// Retries is the number of additional attempts after the first try.
	// Retries=0 means "try once".
	Retries int
}

type Defaults struct {
	Timeout time.Duration

	// Shell is the argv prefix used for ShellCommand hooks.
	// Example: ["/bin/sh", "-lc"].
	Shell []string
}

type ExecResult struct {
	Stdout string
	Stderr string
}

type Result struct {
	Event    Event
	Target   Target
	HookName string

	Attempt int

	Command []string

	StartedAt  time.Time
	FinishedAt time.Time

	Output ExecResult
	Err    error
}
