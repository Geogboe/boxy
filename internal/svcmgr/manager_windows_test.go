//go:build windows

package svcmgr

import "testing"

func TestNewManager_Windows_DispatchesOnUserMode(t *testing.T) {
	priv, err := NewManager(ManagerOptions{UserMode: false})
	if err != nil {
		t.Fatalf("NewManager(UserMode: false): %v", err)
	}
	if _, ok := priv.(*scmManager); !ok {
		t.Fatalf("NewManager(UserMode: false) = %T, want *scmManager", priv)
	}

	user, err := NewManager(ManagerOptions{UserMode: true})
	if err != nil {
		t.Fatalf("NewManager(UserMode: true): %v", err)
	}
	if _, ok := user.(*taskSchedulerManager); !ok {
		t.Fatalf("NewManager(UserMode: true) = %T, want *taskSchedulerManager", user)
	}
}
