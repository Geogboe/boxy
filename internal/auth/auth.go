// Package auth contains operator API-key generation and authentication helpers.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
)

const rawAPIKeyPrefix = "boxy_"

var ErrInvalidCredentials = errors.New("invalid API credentials")

// APIKeyStore is the persistence seam required to authenticate API keys.
type APIKeyStore interface {
	ListAPIKeys(ctx context.Context) ([]model.APIKey, error)
}

// Principal identifies the API key that authenticated a request.
type Principal struct {
	KeyID   model.APIKeyID
	Role    model.APIKeyRole
	Kind    model.APIKeyKind
	Subject string
}

// OwnerIdentity returns the stable identity to record as a resource's
// owner (e.g. Sandbox.OwnerID): the OIDC subject for a personal key, so a
// user's own resources stay visible to them across key rotation (personal
// keys are short-lived by design), or the key ID itself for a service key,
// which has no stable human identity to fall back to. See the OIDC design
// spec's Decision 5.
func (p Principal) OwnerIdentity() string {
	if p.Kind == model.APIKeyKindPersonal && p.Subject != "" {
		return p.Subject
	}
	return string(p.KeyID)
}

// GenerateAPIKey creates a raw API key and its SHA-256 hash. Only the caller
// should ever receive the raw value; the hash is what belongs in persistence.
func GenerateAPIKey() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate API key: %w", err)
	}
	raw = rawAPIKeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashAPIKey(raw), nil
}

// HashAPIKey returns the deterministic storage hash for a raw API key.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Authenticate validates a raw bearer credential against persisted key
// metadata. Expired, revoked, malformed, and unknown keys are indistinguishable
// to callers so the API does not reveal which key records exist.
func Authenticate(ctx context.Context, keys APIKeyStore, raw string, now time.Time) (Principal, error) {
	if keys == nil || raw == "" {
		return Principal{}, ErrInvalidCredentials
	}
	storedHash := HashAPIKey(raw)
	list, err := keys.ListAPIKeys(ctx)
	if err != nil {
		return Principal{}, fmt.Errorf("list API keys: %w", err)
	}
	for _, key := range list {
		if subtle.ConstantTimeCompare([]byte(key.Hash), []byte(storedHash)) != 1 {
			continue
		}
		if !key.Role.Valid() || key.Revoked() || key.Expired(now) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{KeyID: key.ID, Role: key.Role, Kind: key.EffectiveKind(), Subject: key.Subject}, nil
	}
	return Principal{}, ErrInvalidCredentials
}
