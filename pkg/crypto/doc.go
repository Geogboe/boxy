// Package crypto provides cryptographic utilities for Boxy, including
// AES-256-GCM authenticated encryption and secure password generation.
// It is used by storage, core components, and providers to protect
// credentials and other sensitive data.
//
// Overview
//
// The package offers a small surface:
//
//   - Symmetric encryption using AES-256-GCM.
//   - Random key generation.
//   - Secure password generation using crypto/rand.
//
// Encryption guarantees include:
//
//   - 32-byte keys (AES-256).
//   - Unique nonce per encryption (same plaintext → different ciphertext).
//   - Integrity protection via GCM authentication.
//   - Base64-encoded ciphertext for storage.
//
// Password generation guarantees include:
//
//   - crypto/rand as the only randomness source.
//   - Configurable character sets.
//   - Minimum output length of 8 bytes.
//
// API Notes
//
// NewEncryptor constructs an encryptor given a 32-byte key.
// Encrypt and Decrypt perform authenticated encryption and decryption using
// AES-GCM. GenerateKey returns a securely generated 32-byte key.
// Password generation is provided through GeneratePassword and
// GenerateSimplePassword, which produce cryptographically secure random
// strings based on supplied options.
//
// Limitations
//
//   - No key derivation; callers must supply raw keys.
//   - No automated key rotation.
//   - Symmetric encryption only.
//   - Callers must store and manage keys securely.
//
// Dependencies
//
// The package uses only standard library primitives (aes, cipher, rand).
//
// Security Notes
//
// Keys must be handled securely by the caller. The package never logs keys,
// plaintexts, or derived data, and does not perform password hashing or
// asymmetric encryption. For password hashing, use bcrypt/argon2; for KDFs,
// use PBKDF2 or scrypt.
//
// In short, this package provides the minimal cryptographic primitives Boxy
// requires while keeping responsibilities explicit and narrowly scoped.
package crypto
