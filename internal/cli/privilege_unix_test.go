//go:build !windows

package cli

import (
	"os"
	"testing"
)

func TestIsElevated_MatchesEUID(t *testing.T) {
	elevated, err := isElevated()
	if err != nil {
		t.Fatalf("isElevated: %v", err)
	}
	want := os.Geteuid() == 0
	if elevated != want {
		t.Fatalf("isElevated() = %v, want %v (euid=%d)", elevated, want, os.Geteuid())
	}
}
