// Package guestcred contains provider-neutral primitives for generating
// caller-deliverable guest credentials. It does not know how a provider
// installs or consumes the generated value.
package guestcred

import (
	"crypto/rand"
	"fmt"
)

const (
	passwordLength = 24
	letters        = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits         = "0123456789"
	symbols        = "!@#$%^&*()-_=+[]{}"
)

// GenerateRandomPassword returns a cryptographically random password with a
// guaranteed lower-case, upper-case, digit, and symbol character. The result
// is suitable for the default Windows and Linux guest policies Boxy targets.
func GenerateRandomPassword() (string, error) {
	chars := make([]byte, passwordLength)
	classes := []string{letters[:26], letters[26:], digits, symbols}
	for i, class := range classes {
		value, err := randomByte(class)
		if err != nil {
			return "", err
		}
		chars[i] = value
	}
	for i := len(classes); i < len(chars); i++ {
		value, err := randomByte(letters + digits + symbols)
		if err != nil {
			return "", err
		}
		chars[i] = value
	}

	// Fisher-Yates with crypto/rand avoids making the guaranteed class
	// positions predictable.
	for i := len(chars) - 1; i > 0; i-- {
		j, err := randomIndex(i + 1)
		if err != nil {
			return "", err
		}
		chars[i], chars[j] = chars[j], chars[i]
	}
	return string(chars), nil
}

func randomByte(class string) (byte, error) {
	index, err := randomIndex(len(class))
	if err != nil {
		return 0, err
	}
	return class[index], nil
}

func randomIndex(max int) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("random character set is empty")
	}
	limit := make([]byte, 1)
	for {
		if _, err := rand.Read(limit); err != nil {
			return 0, err
		}
		if int(limit[0]) < 256-(256%max) {
			return int(limit[0]) % max, nil
		}
	}
}
