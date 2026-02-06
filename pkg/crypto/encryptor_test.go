package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TRUE UNIT TESTS - No external dependencies

func TestGenerateKey(t *testing.T) {
	t.Run("generates 32 byte key", func(t *testing.T) {
		key, err := GenerateKey()
		require.NoError(t, err)
		assert.Len(t, key, 32)
	})

	t.Run("generates unique keys", func(t *testing.T) {
		key1, err1 := GenerateKey()
		key2, err2 := GenerateKey()

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.NotEqual(t, key1, key2, "keys should be unique")
	})
}

func TestNewEncryptor(t *testing.T) {
	t.Run("accepts valid 32 byte key", func(t *testing.T) {
		key := make([]byte, 32)
		rand.Read(key)

		enc, err := NewEncryptor(key)
		require.NoError(t, err)
		assert.NotNil(t, enc)
	})

	t.Run("rejects short key", func(t *testing.T) {
		key := make([]byte, 16) // Too short
		_, err := NewEncryptor(key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be exactly 32 bytes")
	})

	t.Run("rejects long key", func(t *testing.T) {
		key := make([]byte, 64) // Too long
		_, err := NewEncryptor(key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be exactly 32 bytes")
	})

	t.Run("rejects empty key", func(t *testing.T) {
		_, err := NewEncryptor([]byte{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be exactly 32 bytes")
	})
}

func TestEncryptor_EncryptDecrypt(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)

	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	t.Run("encrypts and decrypts simple string", func(t *testing.T) {
		plaintext := "Hello, World!"

		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)
		assert.NotEmpty(t, ciphertext)
		assert.NotEqual(t, plaintext, ciphertext)

		decrypted, err := enc.Decrypt(ciphertext)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("encrypts and decrypts password", func(t *testing.T) {
		plaintext := "SuperSecretPassword123!"

		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		decrypted, err := enc.Decrypt(ciphertext)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("encrypts and decrypts unicode", func(t *testing.T) {
		plaintext := "Hello 世界 🌍"

		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		decrypted, err := enc.Decrypt(ciphertext)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("encrypts and decrypts long string", func(t *testing.T) {
		plaintext := strings.Repeat("a", 10000)

		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		decrypted, err := enc.Decrypt(ciphertext)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("handles empty string", func(t *testing.T) {
		ciphertext, err := enc.Encrypt("")
		require.NoError(t, err)
		assert.Empty(t, ciphertext)

		decrypted, err := enc.Decrypt("")
		require.NoError(t, err)
		assert.Empty(t, decrypted)
	})

	t.Run("produces different ciphertext each time (nonce)", func(t *testing.T) {
		plaintext := "same message"

		ciphertext1, err1 := enc.Encrypt(plaintext)
		ciphertext2, err2 := enc.Encrypt(plaintext)

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.NotEqual(t, ciphertext1, ciphertext2, "ciphertext should differ due to random nonce")

		// But both decrypt to same plaintext
		decrypted1, _ := enc.Decrypt(ciphertext1)
		decrypted2, _ := enc.Decrypt(ciphertext2)
		assert.Equal(t, plaintext, decrypted1)
		assert.Equal(t, plaintext, decrypted2)
	})
}

func TestEncryptor_DecryptErrors(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)

	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	t.Run("invalid base64", func(t *testing.T) {
		_, err := enc.Decrypt("not-valid-base64!@#$")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode base64")
	})

	t.Run("ciphertext too short", func(t *testing.T) {
		// Valid base64 but too short to contain nonce
		_, err := enc.Decrypt("YWJj") // "abc" in base64
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ciphertext too short")
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		plaintext := "secret"
		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		// Tamper at the byte level so base64 encoding stays valid.
		// This tests GCM's authentication tag — it should detect the
		// corruption and return a decryption error, not a base64 error.
		raw, err := base64.StdEncoding.DecodeString(ciphertext)
		require.NoError(t, err)
		raw[len(raw)-1] ^= 0xff
		tampered := base64.StdEncoding.EncodeToString(raw)

		_, err = enc.Decrypt(tampered)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decrypt")
	})

	t.Run("wrong key", func(t *testing.T) {
		plaintext := "secret"
		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		// Create new encryptor with different key
		wrongKey, _ := GenerateKey()
		wrongEnc, _ := NewEncryptor(wrongKey)

		_, err = wrongEnc.Decrypt(ciphertext)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decrypt")
	})
}

func TestEncryptor_Properties(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)

	enc, err := NewEncryptor(key)
	require.NoError(t, err)

	t.Run("ciphertext is base64 encoded", func(t *testing.T) {
		plaintext := "test"
		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		// Should only contain base64 characters
		for _, ch := range ciphertext {
			assert.True(t,
				(ch >= 'A' && ch <= 'Z') ||
					(ch >= 'a' && ch <= 'z') ||
					(ch >= '0' && ch <= '9') ||
					ch == '+' || ch == '/' || ch == '=',
				"ciphertext should be valid base64")
		}
	})

	t.Run("ciphertext is longer than plaintext", func(t *testing.T) {
		plaintext := "short"
		ciphertext, err := enc.Encrypt(plaintext)
		require.NoError(t, err)

		// Ciphertext includes nonce + auth tag + base64 overhead
		assert.Greater(t, len(ciphertext), len(plaintext))
	})
}
