package pool

import (
	"testing"
)

func TestGenerateRandomPassword(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"short password", 8},
		{"medium password", 16},
		{"long password", 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			password := generateRandomPassword(tt.length)

			// Verify length
			if len(password) != tt.length {
				t.Errorf("expected length %d, got %d", tt.length, len(password))
			}

			// Verify contains only valid charset
			const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
			for _, char := range password {
				found := false
				for _, validChar := range charset {
					if char == validChar {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("invalid character %c in password", char)
				}
			}
		})
	}
}

func TestGenerateRandomPassword_Uniqueness(t *testing.T) {
	// Generate 100 passwords and ensure they're all unique
	// This tests randomness (though not cryptographic strength)
	passwords := make(map[string]bool)
	length := 16
	iterations := 100

	for i := 0; i < iterations; i++ {
		password := generateRandomPassword(length)
		if passwords[password] {
			t.Errorf("generated duplicate password: %s", password)
		}
		passwords[password] = true
	}

	if len(passwords) != iterations {
		t.Errorf("expected %d unique passwords, got %d", iterations, len(passwords))
	}
}

func TestGenerateRandomPassword_NoPanic(t *testing.T) {
	// Ensure it doesn't panic under normal operation
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("generateRandomPassword panicked: %v", r)
		}
	}()

	for i := 0; i < 10; i++ {
		_ = generateRandomPassword(16)
	}
}
