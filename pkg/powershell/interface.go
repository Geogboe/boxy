package powershell

import "context"

// Commander is an interface for executing PowerShell commands
// This allows for mocking in tests
type Commander interface {
	Exec(ctx context.Context, script string) (string, error)
	ExecJSON(ctx context.Context, script string, result interface{}) error
}

// Ensure Executor implements Commander
var _ Commander = (*Executor)(nil)
