package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// cliOIDCConfig mirrors internal/server's GET /auth/cli-config response:
// just enough for the CLI to run its own OIDC grant directly against the
// provider, never through boxy's server.
type cliOIDCConfig struct {
	Issuer      string `json:"issuer"`
	CLIClientID string `json:"cli_client_id"`
}

type oidcExchangeResponse struct {
	Key string `json:"key"`
}

// runOIDCLogin implements `boxy login --oidc`: an OIDC grant against the
// server's configured provider (device-code by default, loopback-redirect
// with --web), then exchanges the resulting ID token for a self-service
// personal API key (see docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-
// auth-design.md's Decisions 4 and 6). Unlike a directly-supplied
// --api-key, this never requires an admin to issue anything on the
// caller's behalf.
func runOIDCLogin(ctx context.Context, opts loginOptions, out, errOut io.Writer) error {
	base := apiBaseURL(opts.server)
	var caPEM []byte
	if opts.caCert != "" {
		data, err := os.ReadFile(opts.caCert) //nolint:gosec // the path is explicitly supplied by --ca-cert or BOXY_CA_CERT.
		if err != nil {
			return fmt.Errorf("read CA certificate: %w", err)
		}
		caPEM = data
	}
	client := apiClientWithMaterial(base, "", caPEM, opts.insecure)

	cfg, err := fetchJSON[cliOIDCConfig](ctx, client, base+"/auth/cli-config")
	if err != nil {
		return fmt.Errorf("fetch CLI OIDC config (is server.oidc.cli_client_id configured?): %w", err)
	}
	if strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.CLIClientID) == "" {
		return fmt.Errorf("server did not return a usable CLI OIDC config")
	}

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return fmt.Errorf("discover OIDC issuer %q: %w", cfg.Issuer, err)
	}

	var rawIDToken string
	if opts.web {
		rawIDToken, err = loopbackOIDCLogin(ctx, provider, cfg.CLIClientID, out)
	} else {
		rawIDToken, err = deviceCodeOIDCLogin(ctx, provider, cfg.CLIClientID, out)
	}
	if err != nil {
		return err
	}

	exchangeResp, err := postJSON[map[string]string, oidcExchangeResponse](ctx, client, base+"/api/v1/api-keys/oidc-exchange", map[string]string{"id_token": rawIDToken})
	if err != nil {
		return fmt.Errorf("exchange OIDC identity for a Boxy API key: %w", err)
	}
	if exchangeResp.Key == "" {
		return fmt.Errorf("server returned an empty API key")
	}

	store := loginCredentialsStore()
	if err := store.Set(base, exchangeResp.Key); err != nil {
		return fmt.Errorf("store API key: %w", err)
	}
	if len(caPEM) != 0 {
		if err := store.SetCA(base, caPEM); err != nil {
			return fmt.Errorf("store CA certificate: %w", err)
		}
	}
	if err := loginClientConfigStore(base); err != nil {
		return fmt.Errorf("store client server default (API key was stored successfully): %w", err)
	}
	_, _ = fmt.Fprintf(out, "Logged in to %s via OIDC\n", base)
	_ = errOut
	return nil
}

// deviceCodeOIDCLogin runs RFC 8628's device-code grant: it works
// headless, with no browser co-located with the CLI process, which is the
// common case for boxy agents on remote/headless Windows Hyper-V hosts
// (see Decision 6). This is `boxy login --oidc`'s default.
func deviceCodeOIDCLogin(ctx context.Context, provider *oidc.Provider, clientID string, out io.Writer) (string, error) {
	deviceCfg := oauth2.Config{
		ClientID: clientID, // public client -- no secret, see Decision 6
		Endpoint: provider.Endpoint(),
		Scopes:   []string{oidc.ScopeOpenID},
	}

	deviceAuth, err := deviceCfg.DeviceAuth(ctx)
	if err != nil {
		return "", fmt.Errorf("start device authorization: %w", err)
	}
	if deviceAuth.VerificationURIComplete != "" {
		_, _ = fmt.Fprintf(out, "Open %s to log in.\n", deviceAuth.VerificationURIComplete)
	} else {
		_, _ = fmt.Fprintf(out, "Open %s and enter code: %s\n", deviceAuth.VerificationURI, deviceAuth.UserCode)
	}
	_, _ = fmt.Fprintln(out, "Waiting for login to complete...")

	token, err := deviceCfg.DeviceAccessToken(ctx, deviceAuth)
	if err != nil {
		return "", fmt.Errorf("complete device login: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", fmt.Errorf("provider did not return an id_token")
	}
	return rawIDToken, nil
}

// loopbackLoginTimeout bounds how long loopbackOIDCLogin waits for the
// browser round trip to complete before giving up.
const loopbackLoginTimeout = 5 * time.Minute

// loopbackOIDCLogin runs the gh-auth-login-style flow: a local
// 127.0.0.1:<port> HTTP server receives the provider's redirect, and the
// system browser is auto-launched at the authorization URL. Opted into via
// --web (see Decision 6) -- it requires a browser co-located with the CLI
// process on the same network-local host, which loopback-redirect breaks
// without. PKCE (S256) replaces a client secret: the CLI is a public
// client with no secret to present.
func loopbackOIDCLogin(ctx context.Context, provider *oidc.Provider, clientID string, out io.Writer) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start local callback listener: %w", err)
	}
	defer listener.Close() //nolint:errcheck // best-effort cleanup; the OS reclaims the port regardless.

	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", listener.Addr().(*net.TCPAddr).Port)
	oauthCfg := oauth2.Config{
		ClientID:    clientID,
		Endpoint:    provider.Endpoint(),
		RedirectURL: redirectURL,
		Scopes:      []string{oidc.ScopeOpenID},
	}

	state, err := randomURLSafeToken()
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()

	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case query.Get("error") != "":
			resultCh <- callbackResult{err: fmt.Errorf("authorization failed: %s", query.Get("error"))}
			writeLoopbackCallbackPage(w, false)
		case query.Get("state") != state:
			resultCh <- callbackResult{err: errors.New("state mismatch on OIDC callback")}
			writeLoopbackCallbackPage(w, false)
		case query.Get("code") == "":
			resultCh <- callbackResult{err: errors.New("no authorization code in OIDC callback")}
			writeLoopbackCallbackPage(w, false)
		default:
			resultCh <- callbackResult{code: query.Get("code")}
			writeLoopbackCallbackPage(w, true)
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	authURL := oauthCfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	_, _ = fmt.Fprintf(out, "Open %s to log in.\n", authURL)
	if err := openBrowser(authURL); err != nil {
		_, _ = fmt.Fprintln(out, "Could not auto-launch a browser; open the URL above manually.")
	}
	_, _ = fmt.Fprintln(out, "Waiting for login to complete...")

	waitCtx, cancel := context.WithTimeout(ctx, loopbackLoginTimeout)
	defer cancel()

	var result callbackResult
	select {
	case result = <-resultCh:
	case <-waitCtx.Done():
		return "", fmt.Errorf("timed out waiting for the OIDC login callback")
	}
	if result.err != nil {
		return "", result.err
	}

	token, err := oauthCfg.Exchange(waitCtx, result.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", fmt.Errorf("exchange authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", fmt.Errorf("provider did not return an id_token")
	}
	return rawIDToken, nil
}

func writeLoopbackCallbackPage(w http.ResponseWriter, ok bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><body><p>Login complete. You can close this tab and return to the terminal.</p></body></html>")
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, "<!DOCTYPE html><html><body><p>Login failed. Return to the terminal and try again.</p></body></html>")
}

func randomURLSafeToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
