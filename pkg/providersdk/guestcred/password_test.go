package guestcred_test

import (
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk/guestcred"
)

func TestGenerateRandomPasswordMeetsGuestPolicyShape(t *testing.T) {
	password, err := guestcred.GenerateRandomPassword()
	if err != nil {
		t.Fatalf("GenerateRandomPassword: %v", err)
	}
	if len(password) != 24 {
		t.Fatalf("password length = %d, want 24", len(password))
	}
	for _, required := range []string{"abcdefghijklmnopqrstuvwxyz", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "0123456789", "!@#$%^&*()-_=+[]{}"} {
		if !strings.ContainsAny(password, required) {
			t.Fatalf("password %q does not contain a character from %q", password, required)
		}
	}
}
