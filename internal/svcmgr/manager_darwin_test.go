//go:build darwin

package svcmgr

import "testing"

func TestNewManager_DarwinIsUnsupported(t *testing.T) {
	_, err := NewManager(ManagerOptions{})
	if err == nil {
		t.Fatal("expected an error on darwin, got nil")
	}
}
