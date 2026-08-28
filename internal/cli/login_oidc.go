package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// cliOIDCConfig mirrors internal/server's GET /auth/cli-config response:
// just enough for the CLI to run its own device-code grant directly
// against the provider, never through boxy's server.
type cliOIDCConfig struct {
	Issuer      string `json:"issuer"`
	CLIClientID string `json:"cli_client_id"`
}

type oidcExchangeResponse struct {
	Key string `json:"key"`
}

// runOIDCLogin implements `boxy login --oidc`: RFC 8628 device-code grant
// against the server's configured OIDC provider, then exchanges the
// resulting ID token for a self-service personal API key (see
// docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md's
// Decisions 4 and 6). Unlike a directly-supplied --api-key, this never
// requires an admin to issue anything on the caller's behalf.
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
	deviceCfg := oauth2.Config{
		ClientID: cfg.CLIClientID, // public client -- no secret, see docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md's Decision 6
		Endpoint: provider.Endpoint(),
		Scopes:   []string{oidc.ScopeOpenID},
	}

	deviceAuth, err := deviceCfg.DeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}
	if deviceAuth.VerificationURIComplete != "" {
		_, _ = fmt.Fprintf(out, "Open %s to log in.\n", deviceAuth.VerificationURIComplete)
	} else {
		_, _ = fmt.Fprintf(out, "Open %s and enter code: %s\n", deviceAuth.VerificationURI, deviceAuth.UserCode)
	}
	_, _ = fmt.Fprintln(out, "Waiting for login to complete...")

	token, err := deviceCfg.DeviceAccessToken(ctx, deviceAuth)
	if err != nil {
		return fmt.Errorf("complete device login: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return fmt.Errorf("provider did not return an id_token")
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
