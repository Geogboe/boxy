package model_test

import (
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

func TestSandboxStatus_IsTransient(t *testing.T) {
	cases := []struct {
		status model.SandboxStatus
		want   bool
	}{
		{model.SandboxStatusPending, true},
		{model.SandboxStatusProvisioning, true},
		{model.SandboxStatusDeleting, true},
		{model.SandboxStatusReady, false},
		{model.SandboxStatusFailed, false},
		{model.SandboxStatus("unknown"), false},
	}
	for _, tc := range cases {
		if got := tc.status.IsTransient(); got != tc.want {
			t.Errorf("SandboxStatus(%q).IsTransient() = %v, want %v", tc.status, got, tc.want)
		}
	}
}
