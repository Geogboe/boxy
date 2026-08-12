//go:build windows

package cli

import (
	"testing"
)

func TestIsElevated_Callable(t *testing.T) {
	_, err := isElevated()
	if err != nil {
		t.Fatalf("isElevated: %v", err)
	}
	// Note: We cannot assert the boolean value on Windows (elevation state
	// is not controllable in a test process). This test simply verifies that
	// the function is callable and never returns an error.
}
