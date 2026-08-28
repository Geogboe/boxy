package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/coreos/go-oidc/v3/oidc"
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

func TestLoginCommand_RejectsWebWithoutOIDC(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"login", "--web", "--api-key", "${BOXY_TEST_API_KEY}"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error using --web without --oidc")
	}
}

// fakeAuthCodeOIDCProvider is a minimal OIDC provider for testing the
// CLI's loopback-redirect grant (--web): discovery, an authorization
// endpoint that immediately redirects back to whatever redirect_uri it was
// given (simulating a browser completing an interactive login instantly),
// and a token endpoint that always succeeds.
type fakeAuthCodeOIDCProvider struct {
	server *httptest.Server
}

func newFakeAuthCodeOIDCProvider(t *testing.T) *fakeAuthCodeOIDCProvider {
	t.Helper()
	p := &fakeAuthCodeOIDCProvider{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.server.URL,
			"authorization_endpoint":                p.server.URL + "/authorize",
			"token_endpoint":                        p.server.URL + "/token",
			"jwks_uri":                              p.server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		cb, err := url.Parse(redirectURI)
		if err != nil {
			http.Error(w, "bad redirect_uri", http.StatusBadRequest)
			return
		}
		vals := cb.Query()
		vals.Set("state", r.URL.Query().Get("state"))
		vals.Set("code", "test-auth-code")
		cb.RawQuery = vals.Encode()
		http.Redirect(w, r, cb.String(), http.StatusFound)
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

func (p *fakeAuthCodeOIDCProvider) URL() string { return p.server.URL }

func TestRunOIDCLoginWeb_StoresExchangedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	provider := newFakeAuthCodeOIDCProvider(t)

	oldOpenBrowser := openBrowser
	openBrowser = func(authURL string) error {
		// Stands in for a real browser: follows the provider's redirect
		// chain (GET /authorize -> 302 -> the loopback callback server)
		// the same way a person clicking the printed link would.
		go func() {
			resp, err := http.Get(authURL) //nolint:gosec,noctx // test-only fake browser hitting a localhost-bound httptest server
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { openBrowser = oldOpenBrowser })

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
	err := runOIDCLogin(context.Background(), loginOptions{server: boxyServer.URL, insecure: true, web: true}, out, &strings.Builder{})
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
}

// syncBuffer is a goroutine-safe io.Writer/String() buffer, for tests that
// need to poll printed output from one goroutine while another goroutine
// (loopbackOIDCLogin, here) is still writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestLoopbackOIDCLogin_DuplicateCallbackDoesNotHang(t *testing.T) {
	provider := newFakeAuthCodeOIDCProvider(t)
	oidcProvider, err := oidc.NewProvider(context.Background(), provider.URL())
	if err != nil {
		t.Fatalf("oidc.NewProvider: %v", err)
	}

	// A plain strings.Builder isn't safe for concurrent use: this test
	// polls out.String() from the main goroutine while loopbackOIDCLogin
	// writes to it from its own goroutine, so a synchronized writer is
	// required (a bare *strings.Builder produced a real data race here,
	// caught by `go test -race`).
	out := &syncBuffer{}
	type loginResult struct {
		token string
		err   error
	}
	resultCh := make(chan loginResult, 1)
	go func() {
		token, err := loopbackOIDCLogin(context.Background(), oidcProvider, "boxy-cli", out)
		resultCh <- loginResult{token, err}
	}()

	var authURL string
	deadline := time.Now().Add(5 * time.Second)
	for authURL == "" && time.Now().Before(deadline) {
		for line := range strings.SplitSeq(out.String(), "\n") {
			if after, ok := strings.CutPrefix(line, "Open "); ok {
				if fields := strings.Fields(after); len(fields) > 0 {
					authURL = fields[0]
				}
			}
		}
		if authURL == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if authURL == "" {
		t.Fatalf("did not see an authorization URL printed, out = %q", out.String())
	}

	// Simulate the callback being hit twice -- a browser refresh/retry --
	// before consuming the result. A blocking channel send in the
	// callback handler would hang the second request (and this test)
	// until loopbackLoginTimeout.
	for i := range 2 {
		resp, err := http.Get(authURL) //nolint:gosec,noctx // test-only fake browser hitting a localhost-bound httptest server
		if err != nil {
			t.Fatalf("GET authURL (hit %d): %v", i, err)
		}
		_ = resp.Body.Close()
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("loopbackOIDCLogin: %v", result.err)
		}
		if result.token != "test-id-token" {
			t.Fatalf("token = %q, want test-id-token", result.token)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loopbackOIDCLogin did not return after duplicate callback hits -- likely blocked on a channel send")
	}
}
