package crypto

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

const (
	// DefaultPasswordLength is the default length for generated passwords
	DefaultPasswordLength = 24

	// Character sets for password generation
	lowerChars   = "abcdefghijklmnopqrstuvwxyz"
	upperChars   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitChars   = "0123456789"
	specialChars = "!@#$%^&*()-_=+[]{}|;:,.<>?"
)

// PasswordOptions configures password generation
type PasswordOptions struct {
	Length         int
	IncludeLower   bool
	IncludeUpper   bool
	IncludeDigits  bool
	IncludeSpecial bool
}

// DefaultPasswordOptions returns secure default options
func DefaultPasswordOptions() PasswordOptions {
	return PasswordOptions{
		Length:         DefaultPasswordLength,
		IncludeLower:   true,
		IncludeUpper:   true,
		IncludeDigits:  true,
		IncludeSpecial: true,
	}
}

// GeneratePassword generates a cryptographically secure random password
// Uses crypto/rand for security (not math/rand)
func GeneratePassword(opts PasswordOptions) (string, error) {
	if opts.Length < 8 {
		return "", fmt.Errorf("password length must be at least 8 characters")
	}

	// Build character set
	charset := ""
	if opts.IncludeLower {
		charset += lowerChars
	}
	if opts.IncludeUpper {
		charset += upperChars
	}
	if opts.IncludeDigits {
		charset += digitChars
	}
	if opts.IncludeSpecial {
		charset += specialChars
	}

	if charset == "" {
		return "", fmt.Errorf("at least one character type must be enabled")
	}

	// Generate password
	password := make([]byte, opts.Length)
	charsetLen := big.NewInt(int64(len(charset)))

	for i := 0; i < opts.Length; i++ {
		// Use crypto/rand for security
		randomIndex, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("failed to generate random number: %w", err)
		}
		password[i] = charset[randomIndex.Int64()]
	}

	return string(password), nil
}

// GenerateSimplePassword generates a password with default options
func GenerateSimplePassword() (string, error) {
	return GeneratePassword(DefaultPasswordOptions())
}
