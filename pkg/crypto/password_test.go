package crypto

import (
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TRUE UNIT TESTS - Pure logic, no external dependencies

func TestDefaultPasswordOptions(t *testing.T) {
	opts := DefaultPasswordOptions()

	assert.Equal(t, DefaultPasswordLength, opts.Length)
	assert.True(t, opts.IncludeLower)
	assert.True(t, opts.IncludeUpper)
	assert.True(t, opts.IncludeDigits)
	assert.True(t, opts.IncludeSpecial)
}

func TestGeneratePassword(t *testing.T) {
	t.Run("generates password of correct length", func(t *testing.T) {
		opts := PasswordOptions{
			Length:         16,
			IncludeLower:   true,
			IncludeUpper:   true,
			IncludeDigits:  true,
			IncludeSpecial: false,
		}

		password, err := GeneratePassword(opts)
		require.NoError(t, err)
		assert.Len(t, password, 16)
	})

	t.Run("generates unique passwords", func(t *testing.T) {
		opts := DefaultPasswordOptions()

		password1, err1 := GeneratePassword(opts)
		password2, err2 := GeneratePassword(opts)

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.NotEqual(t, password1, password2, "passwords should be unique")
	})

	t.Run("rejects short passwords", func(t *testing.T) {
		opts := PasswordOptions{
			Length:       7, // Too short
			IncludeLower: true,
		}

		_, err := GeneratePassword(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least 8 characters")
	})

	t.Run("rejects no character types", func(t *testing.T) {
		opts := PasswordOptions{
			Length:         16,
			IncludeLower:   false,
			IncludeUpper:   false,
			IncludeDigits:  false,
			IncludeSpecial: false,
		}

		_, err := GeneratePassword(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one character type")
	})

	t.Run("lowercase only", func(t *testing.T) {
		opts := PasswordOptions{
			Length:         20,
			IncludeLower:   true,
			IncludeUpper:   false,
			IncludeDigits:  false,
			IncludeSpecial: false,
		}

		password, err := GeneratePassword(opts)
		require.NoError(t, err)

		for _, ch := range password {
			assert.True(t, unicode.IsLower(ch), "should only contain lowercase")
		}
	})

	t.Run("uppercase only", func(t *testing.T) {
		opts := PasswordOptions{
			Length:         20,
			IncludeLower:   false,
			IncludeUpper:   true,
			IncludeDigits:  false,
			IncludeSpecial: false,
		}

		password, err := GeneratePassword(opts)
		require.NoError(t, err)

		for _, ch := range password {
			assert.True(t, unicode.IsUpper(ch), "should only contain uppercase")
		}
	})

	t.Run("digits only", func(t *testing.T) {
		opts := PasswordOptions{
			Length:         20,
			IncludeLower:   false,
			IncludeUpper:   false,
			IncludeDigits:  true,
			IncludeSpecial: false,
		}

		password, err := GeneratePassword(opts)
		require.NoError(t, err)

		for _, ch := range password {
			assert.True(t, unicode.IsDigit(ch), "should only contain digits")
		}
	})

	t.Run("special characters only", func(t *testing.T) {
		opts := PasswordOptions{
			Length:         20,
			IncludeLower:   false,
			IncludeUpper:   false,
			IncludeDigits:  false,
			IncludeSpecial: true,
		}

		password, err := GeneratePassword(opts)
		require.NoError(t, err)

		for _, ch := range password {
			assert.Contains(t, specialChars, string(ch), "should only contain special chars")
		}
	})

	t.Run("mixed character types", func(t *testing.T) {
		opts := DefaultPasswordOptions()
		opts.Length = 100 // Longer to ensure all types appear

		password, err := GeneratePassword(opts)
		require.NoError(t, err)

		hasLower := false
		hasUpper := false
		hasDigit := false
		hasSpecial := false

		for _, ch := range password {
			if unicode.IsLower(ch) {
				hasLower = true
			}
			if unicode.IsUpper(ch) {
				hasUpper = true
			}
			if unicode.IsDigit(ch) {
				hasDigit = true
			}
			if strings.ContainsRune(specialChars, ch) {
				hasSpecial = true
			}
		}

		// With 100 chars, very likely to have all types
		assert.True(t, hasLower, "should contain lowercase")
		assert.True(t, hasUpper, "should contain uppercase")
		assert.True(t, hasDigit, "should contain digits")
		assert.True(t, hasSpecial, "should contain special chars")
	})

	t.Run("minimum length", func(t *testing.T) {
		opts := PasswordOptions{
			Length:       8,
			IncludeLower: true,
		}

		password, err := GeneratePassword(opts)
		require.NoError(t, err)
		assert.Len(t, password, 8)
	})

	t.Run("very long password", func(t *testing.T) {
		opts := PasswordOptions{
			Length:       128,
			IncludeLower: true,
		}

		password, err := GeneratePassword(opts)
		require.NoError(t, err)
		assert.Len(t, password, 128)
	})
}

func TestGenerateSimplePassword(t *testing.T) {
	t.Run("generates default password", func(t *testing.T) {
		password, err := GenerateSimplePassword()
		require.NoError(t, err)
		assert.Len(t, password, DefaultPasswordLength)
	})

	t.Run("generates unique passwords", func(t *testing.T) {
		password1, err1 := GenerateSimplePassword()
		password2, err2 := GenerateSimplePassword()

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.NotEqual(t, password1, password2)
	})
}

// Property-based tests
func TestGeneratePassword_Properties(t *testing.T) {
	t.Run("never produces empty password", func(t *testing.T) {
		opts := PasswordOptions{
			Length:       8,
			IncludeLower: true,
		}

		for i := 0; i < 100; i++ {
			password, err := GeneratePassword(opts)
			require.NoError(t, err)
			assert.NotEmpty(t, password)
		}
	})

	t.Run("always respects length", func(t *testing.T) {
		for length := 8; length <= 64; length += 4 {
			opts := PasswordOptions{
				Length:       length,
				IncludeLower: true,
			}

			password, err := GeneratePassword(opts)
			require.NoError(t, err)
			assert.Len(t, password, length)
		}
	})

	t.Run("only uses requested character types", func(t *testing.T) {
		testCases := []struct {
			name string
			opts PasswordOptions
			test func(rune) bool
		}{
			{
				name: "lowercase only",
				opts: PasswordOptions{Length: 20, IncludeLower: true},
				test: unicode.IsLower,
			},
			{
				name: "uppercase only",
				opts: PasswordOptions{Length: 20, IncludeUpper: true},
				test: unicode.IsUpper,
			},
			{
				name: "digits only",
				opts: PasswordOptions{Length: 20, IncludeDigits: true},
				test: unicode.IsDigit,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				password, err := GeneratePassword(tc.opts)
				require.NoError(t, err)

				for _, ch := range password {
					switch tc.name {
					case "lowercase only":
						assert.True(t, unicode.IsLower(ch))
					case "uppercase only":
						assert.True(t, unicode.IsUpper(ch))
					case "digits only":
						assert.True(t, unicode.IsDigit(ch))
					}
				}
			})
		}
	})
}
