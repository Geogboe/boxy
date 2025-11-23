# pkg/crypto

Cryptographic utilities for secure credential management and data encryption.

## Table of Contents

- [Purpose](#purpose)
- [Contract](#contract)
- [Usage Example](#usage-example)
  - [Encryption](#encryption)
  - [Password Generation](#password-generation)
- [Architecture](#architecture)
- [Testing](#testing)
- [Development](#development)
- [Security Considerations](#security-considerations)
- [Future Enhancements](#future-enhancements)

## Purpose

Provide secure encryption (AES-256-GCM) and password generation using `crypto/rand` for Boxy's credential management needs.

## Contract

**Input:**

- Encryption key (32 bytes for AES-256)
- Plaintext data or password generation options

**Output:**

- Base64-encoded ciphertext (for storage)
- Cryptographically secure random passwords
- Decrypted plaintext

**Guarantees:**

- Uses `crypto/rand` (NOT `math/rand`) for security
- AES-256-GCM authenticated encryption
- Unique nonce per encryption (same plaintext → different ciphertext)
- Tamper detection (GCM authentication)
- Password length >= 8 characters

**Limitations:**

- Keys must be exactly 32 bytes
- No key derivation (provide your own key)
- No key rotation (manual process)
- Symmetric encryption only

## Usage Example

### Encryption

```go
import "github.com/Geogboe/boxy/pkg/crypto" // adjust if module path differs

// Generate encryption key
key, err := crypto.GenerateKey()
if err != nil {
    log.Fatal(err)
}

// Create encryptor
enc, err := crypto.NewEncryptor(key)
if err != nil {
    log.Fatal(err)
}

// Encrypt password
password := "SecretPassword123"
ciphertext, err := enc.Encrypt(password)
if err != nil {
    log.Fatal(err)
}

// Store ciphertext in database...

// Later, decrypt
plaintext, err := enc.Decrypt(ciphertext)
```

### Password Generation

```go
import "github.com/Geogboe/boxy/pkg/crypto" // adjust if module path differs

// Simple: use defaults (24 chars, mixed case, digits, special)
password, err := crypto.GenerateSimplePassword()
if err != nil {
    log.Fatal(err)
}

// Custom: configure options
opts := crypto.PasswordOptions{
    Length:         32,
    IncludeLower:   true,
    IncludeUpper:   true,
    IncludeDigits:  true,
    IncludeSpecial: false, // No special chars
}
password, err := crypto.GeneratePassword(opts)
```

## Architecture

**Links:**

- [Package Restructure Plan](../../docs/planning/PACKAGE_RESTRUCTURE.md)
- [V1 Implementation Plan](../../docs/V1_IMPLEMENTATION_PLAN.md) - See "Credential Management"

**Dependencies:**

- Standard library only: `crypto/aes`, `crypto/cipher`, `crypto/rand`

**Used by:**

- `internal/core/pool` - Encrypt resource credentials
- `internal/storage` - Store encrypted data in database
- `pkg/provider/*` - Generate passwords for resources

## Testing

### Unit Tests (100% coverage)

All tests are **true unit tests** - no external dependencies, pure logic:

```bash
go test ./pkg/crypto
go test -v -cover ./pkg/crypto
```

**Coverage:**

- ✅ Encryptor creation (valid/invalid keys)
- ✅ Encrypt/decrypt roundtrip (various data types)
- ✅ Nonce uniqueness (same input → different output)
- ✅ Tamper detection (modified ciphertext → error)
- ✅ Wrong key detection
- ✅ Password generation (length, character types, uniqueness)
- ✅ Property-based tests (invariants hold across many iterations)

### No Integration Tests Needed

This package has no external dependencies, so unit tests are sufficient.

### CI

Runs on all platforms (Linux, Windows, macOS) - no platform-specific code.

## Development

### Running Tests

```bash
# All tests
go test ./pkg/crypto

# Verbose
go test -v ./pkg/crypto

# With coverage
go test -v -cover ./pkg/crypto

# Coverage report
go test -coverprofile=coverage.out ./pkg/crypto
go tool cover -html=coverage.out
```

## Security Considerations

**✅ Secure Practices:**

- Uses `crypto/rand` (cryptographically secure)
- AES-256-GCM (authenticated encryption)
- Random nonce per encryption
- No hardcoded keys

**⚠️ User Responsibilities:**

- Store encryption keys securely (environment variables, key management service)
- Never log plaintext passwords or keys
- Rotate keys periodically
- Use HTTPS for transmitting credentials

**❌ Not Suitable For:**

- Password hashing (use bcrypt, argon2 instead)
- Key derivation from passwords (use PBKDF2, scrypt instead)
- Asymmetric encryption (use RSA, ECDH instead)

## Future Enhancements

- [ ] Key derivation function (KDF) for password-based keys
- [ ] Key rotation support
- [ ] Multiple encryption algorithms
- [ ] Public/private key encryption
- [ ] Integration with hardware security modules (HSM)

---
If the module path differs from `github.com/Geogboe/boxy`, update the import examples accordingly.
