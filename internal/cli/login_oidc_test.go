package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/credentials"
)

// fakeDeviceOIDCProvider is a minimal OIDC provider for testing the CLI's
// device-code grant: discovery, a device_authorization_endpoint, and a
// token endpoint that immediately succeeds on the first poll (no
// authorization_pending backoff to keep the test fast).
type fakeDeviceOIDCProvider struct {
	server *httptest.Server
}

func newFakeDeviceOIDCProvider(t *testing.T) *fakeDeviceOIDCProvider {
	t.Helper()
	p := &fakeDeviceOIDCProvider{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.server.URL,
			"authorization_endpoint":                p.server.URL + "/authorize",
			"token_endpoint":                        p.server.URL + "/token",
			"device_authorization_endpoint":         p.server.URL + "/device",
			"jwks_uri":                              p.server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("POST /device", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "test-device-code",
			"user_code":        "TEST-CODE",
			"verification_uri": p.server.URL + "/verify",
			"expires_in":       600,
			"interval":         1,
		})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     "test-id-token",
		})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeDeviceOIDCProvider) URL() string { return p.server.URL }

func TestRunOIDCLogin_StoresExchangedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	provider := newFakeDeviceOIDCProvider(t)

	var exchangeIDToken string
	boxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/auth/cli-config":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":        provider.URL(),
				"cli_client_id": "boxy-cli",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/api-keys/oidc-exchange":
			var req struct {
				IDToken string `json:"id_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			exchangeIDToken = req.IDToken
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "boxy_minted-personal-key"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer boxyServer.Close()

	backend := &testCredentialBackend{values: make(map[string]string)}
	credStore := credentials.NewWithBackend("boxy", backend)
	oldFactory := loginCredentialsStore
	loginCredentialsStore = func() *credentials.Store { return credStore }
	t.Cleanup(func() { loginCredentialsStore = oldFactory })

	out := &strings.Builder{}
	err := runOIDCLogin(context.Background(), loginOptions{server: boxyServer.URL, insecure: true}, out, &strings.Builder{})
	if err != nil {
		t.Fatalf("runOIDCLogin: %v", err)
	}

	if exchangeIDToken != "test-id-token" {
		t.Fatalf("exchanged id_token = %q, want test-id-token", exchangeIDToken)
	}
	got, err := credStore.Get(boxyServer.URL)
	if err != nil {
		t.Fatalf("stored credential: %v", err)
	}
	if got != "boxy_minted-personal-key" {
		t.Fatalf("stored credential = %q, want the exchanged key", got)
	}
	if !strings.Contains(out.String(), "Logged in") {
		t.Fatalf("output = %q, want a success message", out.String())
	}
	cfg, err := loadClientConfig()
	if err != nil {
		t.Fatalf("load client config: %v", err)
	}
	if cfg.Server != boxyServer.URL {
		t.Fatalf("client server default = %q, want %q", cfg.Server, boxyServer.URL)
	}
}

func TestRunOIDCLogin_FailsWhenCLIConfigUnavailable(t *testing.T) {
	boxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer boxyServer.Close()

	err := runOIDCLogin(context.Background(), loginOptions{server: boxyServer.URL, insecure: true}, &strings.Builder{}, &strings.Builder{})
	if err == nil {
		t.Fatal("runOIDCLogin succeeded, want an error when /auth/cli-config is unavailable")
	}
}

func TestLoginCommand_RejectsOIDCWithAPIKey(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"login", "--oidc", "--api-key", "${BOXY_TEST_API_KEY}"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error combining --oidc with --api-key")
	}
}
