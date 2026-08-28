package auth

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
)

type fakeSessionStore struct {
	sessions []model.Session
}

func (s *fakeSessionStore) ListSessions(context.Context) ([]model.Session, error) {
	return append([]model.Session(nil), s.sessions...), nil
}

func TestGenerateSessionToken(t *testing.T) {
	raw, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if raw == "" {
		t.Fatal("generated session token is empty")
	}
	if hash == "" || hash == raw {
		t.Fatalf("generated hash must be non-empty and must not equal raw token: %q", hash)
	}
	if HashSessionToken(raw) != hash {
		t.Fatalf("HashSessionToken(raw) = %q, want %q", HashSessionToken(raw), hash)
	}
}

func TestAuthenticateSession(t *testing.T) {
	raw, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	store := &fakeSessionStore{sessions: []model.Session{{
		ID:        "session-1",
		Hash:      hash,
		Kind:      model.SessionKindLocalAdmin,
		Subject:   model.LocalAdminUsername,
		Role:      model.APIKeyRoleAdmin,
		CreatedAt: now,
		ExpiresAt: future,
	}}}

	principal, err := AuthenticateSession(context.Background(), store, raw, now)
	if err != nil {
		t.Fatalf("AuthenticateSession: %v", err)
	}
	if principal.SessionID != "session-1" || principal.Role != model.APIKeyRoleAdmin || principal.Subject != model.LocalAdminUsername {
		t.Fatalf("principal = %+v, want session-1/admin/admin", principal)
	}

	if _, err := AuthenticateSession(context.Background(), store, "not-a-real-token", now); err == nil {
		t.Fatal("AuthenticateSession(invalid) succeeded, want error")
	}
}

func TestAuthenticateSessionRejectsExpiredSession(t *testing.T) {
	raw, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	store := &fakeSessionStore{sessions: []model.Session{{
		ID: "session-1", Hash: hash, Role: model.APIKeyRoleAdmin, ExpiresAt: past,
	}}}

	if _, err := AuthenticateSession(context.Background(), store, raw, now); err == nil {
		t.Fatal("AuthenticateSession(expired) succeeded, want error")
	}
}

func TestAuthenticateSessionRejectsEmptyToken(t *testing.T) {
	store := &fakeSessionStore{}
	if _, err := AuthenticateSession(context.Background(), store, "", time.Now()); err == nil {
		t.Fatal("AuthenticateSession(\"\") succeeded, want error")
	}
}

func TestGenerateBootstrapPassword(t *testing.T) {
	pw1, err := GenerateBootstrapPassword()
	if err != nil {
		t.Fatalf("GenerateBootstrapPassword: %v", err)
	}
	pw2, err := GenerateBootstrapPassword()
	if err != nil {
		t.Fatalf("GenerateBootstrapPassword: %v", err)
	}
	if pw1 == "" {
		t.Fatal("generated bootstrap password is empty")
	}
	if pw1 == pw2 {
		t.Fatal("two generated bootstrap passwords were identical, want independently random")
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" || hash == "correct horse battery staple" {
		t.Fatalf("HashPassword returned an unexpected value: %q", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("VerifyPassword(correct password) = false, want true")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("VerifyPassword(wrong password) = true, want false")
	}
	if VerifyPassword("", "correct horse battery staple") {
		t.Fatal("VerifyPassword with empty hash = true, want false")
	}
	if VerifyPassword(hash, "") {
		t.Fatal("VerifyPassword with empty raw password = true, want false")
	}
}
