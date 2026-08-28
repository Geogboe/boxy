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
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidSession = errors.New("invalid or expired session")

// SessionStore is the persistence seam required to authenticate a web-UI
// session cookie. Deliberately not model.Store itself, the same narrowing
// APIKeyStore already does for API keys.
type SessionStore interface {
	ListSessions(ctx context.Context) ([]model.Session, error)
}

// SessionPrincipal identifies the session that authenticated a web-UI
// request. Deliberately a separate type from Principal (the REST API's
// bearer-key identity): a browser session's ID is not an API key ID, and
// conflating the two ID spaces would misrepresent what actually
// authenticated the request. Session and API-key auth are independent
// mechanisms for independent surfaces (UI vs. REST API) that happen to
// share the same three-role vocabulary.
type SessionPrincipal struct {
	SessionID model.SessionID
	// Subject is the session's underlying identity: the local admin
	// username for a SessionKindLocalAdmin session, or the OIDC subject
	// claim for a SessionKindOIDC session.
	Subject string
	Role    model.APIKeyRole
}

// GenerateSessionToken creates a raw session cookie value and its SHA-256
// hash. Only the caller (the just-authenticated browser) ever receives the
// raw value; the hash is what belongs in persistence. No "boxy_" prefix
// (unlike GenerateAPIKey): a session token is never meant to be typed as a
// bearer credential, so there's no reason to make it look like one.
func GenerateSessionToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashSessionToken(raw), nil
}

// HashSessionToken returns the deterministic storage hash for a raw session
// cookie value.
func HashSessionToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// AuthenticateSession validates a raw session cookie value against
// persisted session records, the same linear-scan-plus-constant-time-
// compare shape as Authenticate for API keys. Expired sessions are
// rejected but not deleted here — expiry sweeping is a separate concern
// (a reconciler, mirroring internal/sandbox/deleter.go's pattern), not
// this read path's job.
func AuthenticateSession(ctx context.Context, sessions SessionStore, raw string, now time.Time) (SessionPrincipal, error) {
	if sessions == nil || raw == "" {
		return SessionPrincipal{}, ErrInvalidSession
	}
	storedHash := HashSessionToken(raw)
	list, err := sessions.ListSessions(ctx)
	if err != nil {
		return SessionPrincipal{}, fmt.Errorf("list sessions: %w", err)
	}
	for _, session := range list {
		if subtle.ConstantTimeCompare([]byte(session.Hash), []byte(storedHash)) != 1 {
			continue
		}
		if !session.Role.Valid() || session.Expired(now) {
			return SessionPrincipal{}, ErrInvalidSession
		}
		return SessionPrincipal{SessionID: session.ID, Subject: session.Subject, Role: session.Role}, nil
	}
	return SessionPrincipal{}, ErrInvalidSession
}

// GenerateBootstrapPassword creates a random, human-typable local-admin
// bootstrap password: 20 bytes of entropy, base64 URL-encoded (no padding),
// so it's copy/paste-safe in a terminal without ambiguous-character
// confusion.
func GenerateBootstrapPassword() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate bootstrap password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashPassword hashes a raw password for storage in
// model.LocalAdminAccount.PasswordHash. bcrypt (via golang.org/x/crypto,
// already a project dependency) rather than a raw SHA-256 hash: unlike an
// API key or session token, a password is user-chosen-entropy-adjacent
// (even a generated bootstrap password may later be replaced by an
// operator-chosen one), so it needs a deliberately slow, salted KDF.
func HashPassword(raw string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether raw matches the given bcrypt hash.
func VerifyPassword(hash, raw string) bool {
	if hash == "" || raw == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)) == nil
}
