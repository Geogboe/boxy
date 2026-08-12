//go:build !windows

package svcmgr

// EncryptToken is the identity function on non-Windows platforms — Linux
// protects the persisted service config file via 0600 file permissions
// instead of at-rest encryption (see docs/superpowers/specs/2026-08-10-service-install-design.md).
func EncryptToken(plaintext []byte) ([]byte, error) { return plaintext, nil }

// DecryptToken is the identity function on non-Windows platforms.
func DecryptToken(ciphertext []byte) ([]byte, error) { return ciphertext, nil }
