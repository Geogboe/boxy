package model_test

import (
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

func TestAPIKeyRoleValid(t *testing.T) {
	tests := []struct {
		role  model.APIKeyRole
		valid bool
	}{
		{model.APIKeyRoleUser, true},
		{model.APIKeyRoleAuditor, true},
		{model.APIKeyRoleAdmin, true},
		{"operator", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := tt.role.Valid(); got != tt.valid {
			t.Errorf("APIKeyRole(%q).Valid() = %v, want %v", tt.role, got, tt.valid)
		}
	}
}
