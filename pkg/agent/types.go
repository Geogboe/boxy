package agent

import "time"

type AgentID string

// Info is the agent's advertised identity and selection attributes.
type Info struct {
	ID           AgentID           `json:"id"`
	Labels       map[string]string `json:"labels,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
}

// Selector is a simple “all key/value pairs must match” selector.
type Selector map[string]string

// Task is a unit of work assigned to an agent.
type Task struct {
	ID string `json:"id"`

	Selector Selector `json:"selector,omitempty"`

	// RequiredCapabilities must be a subset of agent capabilities.
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`

	Action Action `json:"action"`
}

type ActionType string

const (
	ActionTypeExec ActionType = "exec"
)

// Action is a typed unit of local work.
type Action struct {
	Type ActionType  `json:"type"`
	Exec *ExecAction `json:"exec,omitempty"`
}

type ExecAction struct {
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
}

type Result struct {
	TaskID string `json:"task_id"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`

	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}
