package svcmgr

import (
	"errors"
	"testing"
)

func TestSentinelErrors_AreDistinct(t *testing.T) {
	if errors.Is(ErrAlreadyInstalled, ErrNotInstalled) {
		t.Fatal("ErrAlreadyInstalled and ErrNotInstalled must be distinct sentinel errors")
	}
}

func TestStatus_ZeroValueIsNotInstalled(t *testing.T) {
	var st Status
	if st.Installed || st.Running || st.Mode != "" {
		t.Fatalf("zero-value Status should report not-installed, got %+v", st)
	}
}
