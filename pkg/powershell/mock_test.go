package powershell

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are TRUE UNIT TESTS - no external dependencies, pure logic testing

func TestMockExecutor(t *testing.T) {
	t.Run("NewMock creates valid mock", func(t *testing.T) {
		mock := NewMock()
		assert.NotNil(t, mock)
		assert.Empty(t, mock.ExecCalls)
		assert.Empty(t, mock.JSONCalls)
	})

	t.Run("Exec tracks calls", func(t *testing.T) {
		mock := NewMock()
		ctx := context.Background()

		mock.Exec(ctx, "command 1")
		mock.Exec(ctx, "command 2")

		assert.Len(t, mock.ExecCalls, 2)
		assert.Equal(t, "command 1", mock.ExecCalls[0])
		assert.Equal(t, "command 2", mock.ExecCalls[1])
	})

	t.Run("WithExecResponse returns configured value", func(t *testing.T) {
		mock := NewMock().WithExecResponse("test output", nil)
		ctx := context.Background()

		output, err := mock.Exec(ctx, "Get-Test")

		require.NoError(t, err)
		assert.Equal(t, "test output", output)
		assert.Len(t, mock.ExecCalls, 1)
	})

	t.Run("WithExecResponse returns configured error", func(t *testing.T) {
		expectedErr := fmt.Errorf("powershell failed")
		mock := NewMock().WithExecResponse("", expectedErr)
		ctx := context.Background()

		_, err := mock.Exec(ctx, "Get-Invalid")

		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})

	t.Run("ExecJSON tracks calls", func(t *testing.T) {
		mock := NewMock()
		ctx := context.Background()

		var result map[string]interface{}
		mock.ExecJSON(ctx, "json command 1", &result)
		mock.ExecJSON(ctx, "json command 2", &result)

		assert.Len(t, mock.JSONCalls, 2)
		assert.Equal(t, "json command 1", mock.JSONCalls[0])
		assert.Equal(t, "json command 2", mock.JSONCalls[1])
	})

	t.Run("WithJSONResponse returns configured object", func(t *testing.T) {
		testData := map[string]interface{}{
			"Name":  "TestVM",
			"State": "Running",
			"CPU":   4,
		}
		mock := NewMock().WithJSONResponse(testData, nil)
		ctx := context.Background()

		var result map[string]interface{}
		err := mock.ExecJSON(ctx, "Get-VM", &result)

		require.NoError(t, err)
		assert.Equal(t, "TestVM", result["Name"])
		assert.Equal(t, "Running", result["State"])
		assert.Equal(t, float64(4), result["CPU"]) // JSON numbers are float64
	})

	t.Run("WithJSONResponse returns configured error", func(t *testing.T) {
		expectedErr := fmt.Errorf("json parse error")
		mock := NewMock().WithJSONResponse(nil, expectedErr)
		ctx := context.Background()

		var result map[string]interface{}
		err := mock.ExecJSON(ctx, "Get-Invalid", &result)

		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})

	t.Run("WithJSONResponse with typed struct", func(t *testing.T) {
		type VMInfo struct {
			Name  string
			State string
			CPU   int
		}

		testData := VMInfo{
			Name:  "TestVM",
			State: "Running",
			CPU:   2,
		}
		mock := NewMock().WithJSONResponse(testData, nil)
		ctx := context.Background()

		var result VMInfo
		err := mock.ExecJSON(ctx, "Get-VM", &result)

		require.NoError(t, err)
		assert.Equal(t, "TestVM", result.Name)
		assert.Equal(t, "Running", result.State)
		assert.Equal(t, 2, result.CPU)
	})
}

func TestMockExecutor_InterfaceCompliance(t *testing.T) {
	// Ensure MockExecutor implements Commander
	var _ Commander = (*MockExecutor)(nil)
	var _ Commander = NewMock()
}
