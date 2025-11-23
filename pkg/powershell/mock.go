package powershell

import (
	"context"
	"encoding/json"
	"fmt"
)

// MockExecutor is a mock implementation of Commander for testing
type MockExecutor struct {
	// ExecFunc is called when Exec is invoked
	ExecFunc func(ctx context.Context, script string) (string, error)

	// ExecJSONFunc is called when ExecJSON is invoked
	ExecJSONFunc func(ctx context.Context, script string, result interface{}) error

	// Calls stores all calls to Exec for verification
	ExecCalls []string

	// JSONCalls stores all calls to ExecJSON for verification
	JSONCalls []string
}

// NewMock creates a new mock executor
func NewMock() *MockExecutor {
	return &MockExecutor{
		ExecCalls: []string{},
		JSONCalls: []string{},
	}
}

// Exec implements Commander interface
func (m *MockExecutor) Exec(ctx context.Context, script string) (string, error) {
	m.ExecCalls = append(m.ExecCalls, script)

	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, script)
	}

	return "", nil
}

// ExecJSON implements Commander interface
func (m *MockExecutor) ExecJSON(ctx context.Context, script string, result interface{}) error {
	m.JSONCalls = append(m.JSONCalls, script)

	if m.ExecJSONFunc != nil {
		return m.ExecJSONFunc(ctx, script, result)
	}

	return nil
}

// WithExecResponse configures the mock to return a specific response
func (m *MockExecutor) WithExecResponse(output string, err error) *MockExecutor {
	m.ExecFunc = func(ctx context.Context, script string) (string, error) {
		return output, err
	}
	return m
}

// WithJSONResponse configures the mock to return a specific JSON object
func (m *MockExecutor) WithJSONResponse(data interface{}, err error) *MockExecutor {
	m.ExecJSONFunc = func(ctx context.Context, script string, result interface{}) error {
		if err != nil {
			return err
		}

		// Marshal and unmarshal to simulate JSON roundtrip
		jsonBytes, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			return fmt.Errorf("mock marshal error: %w", marshalErr)
		}

		return json.Unmarshal(jsonBytes, result)
	}
	return m
}

// Ensure MockExecutor implements Commander
var _ Commander = (*MockExecutor)(nil)
