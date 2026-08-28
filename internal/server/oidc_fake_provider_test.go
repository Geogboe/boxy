package server_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeOIDCProvider is a minimal in-process OpenID Connect provider for
// integration-testing the real client-side flow (discovery, JWKS fetch,
// token exchange, RS256 signature verification, claims decoding) without
// mocking any of that away and without a third-party JWT library --
// go-oidc's own client code is the thing under test, so the fake only
// needs to speak the wire protocol it expects.
type fakeOIDCProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string

	// pending maps an authorization code to the ID token claims /token
	// should issue when that code is redeemed. Set per test via
	// SetPendingCode before hitting the real server's /auth/callback.
	pending map[string]fakeOIDCClaims
}

// fakeOIDCClaims is the subset of ID token content a test controls.
type fakeOIDCClaims struct {
	Subject string
	Nonce   string
	Extra   map[string]any // e.g. {"groups": []string{"boxy-admins"}}
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	p := &fakeOIDCProvider{key: key, kid: "test-key-1", pending: make(map[string]fakeOIDCClaims)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("GET /keys", p.handleKeys)
	mux.HandleFunc("POST /token", p.handleToken)
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeOIDCProvider) URL() string { return p.server.URL }

// SetPendingCode registers the claims /token should issue when code is
// redeemed. Real providers derive this from the authorization step; tests
// set it directly since they never drive a real browser through /authorize.
func (p *fakeOIDCProvider) SetPendingCode(code string, claims fakeOIDCClaims) {
	p.pending[code] = claims
}

func (p *fakeOIDCProvider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                p.server.URL,
		"authorization_endpoint":                p.server.URL + "/authorize",
		"token_endpoint":                        p.server.URL + "/token",
		"jwks_uri":                              p.server.URL + "/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *fakeOIDCProvider) handleKeys(w http.ResponseWriter, r *http.Request) {
	pub := p.key.PublicKey
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": p.kid,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(eBytes),
		}},
	})
}

func (p *fakeOIDCProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	claims, ok := p.pending[code]
	if !ok {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	delete(p.pending, code) // authorization codes are single-use

	idToken, err := p.signIDToken(claims)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

func (p *fakeOIDCProvider) signIDToken(c fakeOIDCClaims) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": p.kid}
	now := time.Now()
	payload := map[string]any{
		"iss": p.server.URL,
		"sub": c.Subject,
		"aud": []string{"boxy-test-client"},
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	if c.Nonce != "" {
		payload["nonce"] = c.Nonce
	}
	for k, v := range c.Extra {
		payload[k] = v
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal jwt header: %w", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal jwt payload: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
