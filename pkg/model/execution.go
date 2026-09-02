package model

import "time"

// ExecutionID identifies one accepted sandbox execution.
type ExecutionID string

// ExecutionStatus is the control-plane lifecycle of an execution.
type ExecutionStatus string

const (
	ExecutionStatusPending     ExecutionStatus = "pending"
	ExecutionStatusRunning     ExecutionStatus = "running"
	ExecutionStatusSucceeded   ExecutionStatus = "succeeded"
	ExecutionStatusFailed      ExecutionStatus = "failed"
	ExecutionStatusCancelled   ExecutionStatus = "cancelled"
	ExecutionStatusInterrupted ExecutionStatus = "interrupted"
)

// IsTerminal reports whether an execution will receive no more output.
func (s ExecutionStatus) IsTerminal() bool {
	switch s {
	case ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusCancelled, ExecutionStatusInterrupted:
		return true
	default:
		return false
	}
}

// ExecutionInputKind describes the source of an execution request without
// storing the request's sensitive or potentially large content.
type ExecutionInputKind string

const (
	ExecutionInputCommand     ExecutionInputKind = "command"
	ExecutionInputCommandText ExecutionInputKind = "command_text"
	ExecutionInputScript      ExecutionInputKind = "script"
	ExecutionInputStdin       ExecutionInputKind = "stdin"
)

// ExecutionChunk is one immutable, cursor-addressable output chunk. Cursor
// values are assigned by the execution service and are not byte offsets; this
// guarantees reconnects only resume at chunk boundaries.
type ExecutionChunk struct {
	Cursor  uint64 `json:"cursor"`
	Stream  string `json:"stream"`
	Data    []byte `json:"data"`
	Dropped bool   `json:"dropped,omitempty"`
}

// Execution is the durable, safe representation of a sandbox command.
// Deliberately absent are command arguments, script content, environment
// values, and credentials. The in-process worker retains those only until the
// provider call finishes.
type Execution struct {
	ID                 ExecutionID        `json:"id"`
	SandboxID          SandboxID          `json:"sandbox_id"`
	ResourceID         ResourceID         `json:"resource_id"`
	ActorID            string             `json:"actor_id,omitempty"`
	Status             ExecutionStatus    `json:"status"`
	InputKind          ExecutionInputKind `json:"input_kind"`
	RequestFingerprint string             `json:"request_fingerprint"`
	CreatedAt          time.Time          `json:"created_at"`
	StartedAt          *time.Time         `json:"started_at,omitempty"`
	FinishedAt         *time.Time         `json:"finished_at,omitempty"`
	DeadlineAt         time.Time          `json:"deadline_at"`
	ExitCode           *int               `json:"exit_code,omitempty"`
	Error              string             `json:"error,omitempty"`
	Truncated          bool               `json:"truncated,omitempty"`
	Chunks             []ExecutionChunk   `json:"chunks,omitempty"`
}
