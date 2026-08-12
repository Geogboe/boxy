package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/spf13/cobra"
)

type apiError struct {
	StatusCode int
	URL        string
	Message    string
}

func (e *apiError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("request to %s returned HTTP %d: %s", e.URL, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("request to %s returned HTTP %d", e.URL, e.StatusCode)
}

func fetchJSON[T any](ctx context.Context, client *http.Client, url string) (T, error) {
	var zero T

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}

	return doJSON[T](client, req)
}

func postJSON[TReq any, TResp any](ctx context.Context, client *http.Client, url string, body TReq) (TResp, error) {
	var zero TResp

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		return zero, fmt.Errorf("encode request for %s: %w", url, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, buf)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")

	return doJSON[TResp](client, req)
}

func deleteJSON[T any](ctx context.Context, client *http.Client, url string) (T, error) {
	var zero T

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return zero, err
	}

	return doJSON[T](client, req)
}

func doJSON[T any](client *http.Client, req *http.Request) (T, error) {
	var zero T

	resp, err := client.Do(req) //nolint:gosec // CLI requests intentionally target the user-configured Boxy server.
	if err != nil {
		return zero, wrapConnError(err, req.URL.Host)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return zero, decodeAPIError(resp, req.URL.String())
	}

	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return zero, fmt.Errorf("decode response from %s: %w", req.URL.String(), err)
	}

	return v, nil
}

func decodeAPIError(resp *http.Response, url string) error {
	body, _ := io.ReadAll(resp.Body)
	apiErr := &apiError{StatusCode: resp.StatusCode, URL: url}
	if len(body) == 0 {
		return apiErr
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		apiErr.Message = payload.Error
		return apiErr
	}

	apiErr.Message = strings.TrimSpace(string(body))
	return apiErr
}

// wrapConnError classifies err as a daemon-unreachable condition (connection
// refused, dial failure, DNS failure, etc.) at addr and, if so, wraps it with
// a hint that `boxy serve` may not be running. Errors that aren't connection
// failures (HTTP-level errors, decode errors, etc.) are returned unchanged.
func wrapConnError(err error, addr string) error {
	if err == nil {
		return nil
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return err
	}
	return fmt.Errorf("cannot reach server at %s (is `boxy serve` running?): %w", addr, err)
}

// validatePathID trims raw, rejects an empty/whitespace-only value with an
// error naming what was empty, and returns it escaped for safe inclusion as
// a single URL path segment (so a stray "/", "?", or "#" in a hand-typed ID
// can't reroute or reinterpret the request).
func validatePathID(kind, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s must not be empty", kind)
	}
	return url.PathEscape(trimmed), nil
}

func apiBaseURL(server string) string {
	server = strings.TrimSpace(server)
	server = strings.TrimRight(server, "/")
	if server == "" {
		return "https://127.0.0.1:9090"
	}
	if strings.HasPrefix(server, "http://") || strings.HasPrefix(server, "https://") {
		return server
	}
	// An explicit bare address is retained as a local-development HTTP
	// shorthand. Production/default resolution supplies an https:// URL.
	return "http://" + server
}

func defaultAPIClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func apiClientForServer(server string) *http.Client {
	client, _ := apiClientForSettings(server, "", apiInsecureFromEnvironment())
	return client
}

func apiClientForCommand(cmd *cobra.Command, server string) (*http.Client, error) {
	caCertPath, _ := cmd.Flags().GetString("ca-cert")
	insecure, _ := cmd.Flags().GetBool("insecure")
	return apiClientForSettings(server, caCertPath, insecure)
}

func apiClientForSettings(server, caCertPath string, insecure bool) (*http.Client, error) {
	creds := credentials.New()
	key, _ := creds.Get(apiBaseURL(server))
	caPEM, _ := creds.GetCA(apiBaseURL(server))
	if caCertPath != "" {
		data, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}
		caPEM = data
	}
	return apiClientWithMaterial(server, key, caPEM, insecure), nil
}

func apiClientWithCredentials(server string, creds *credentials.Store, insecure bool) *http.Client {
	base := apiBaseURL(server)
	key, _ := creds.Get(base)
	caPEM, _ := creds.GetCA(base)
	return apiClientWithMaterial(base, key, caPEM, insecure)
}

func apiClientWithMaterial(server, key string, caPEM []byte, insecure bool) *http.Client {
	base := apiBaseURL(server)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.HasPrefix(base, "https://") {
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: insecure, //nolint:gosec // only enabled by the explicit --insecure development path.
		}
		if len(caPEM) != 0 {
			roots := x509.NewCertPool()
			if roots.AppendCertsFromPEM(caPEM) {
				transport.TLSClientConfig.RootCAs = roots
			}
		}
	}
	if key != "" {
		return &http.Client{Transport: bearerTransport{base: transport, key: key}, Timeout: 5 * time.Second}
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

type bearerTransport struct {
	base http.RoundTripper
	key  string
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.key)
	return t.base.RoundTrip(clone)
}

func apiInsecureFromEnvironment() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("BOXY_API_INSECURE")))
	return value == "1" || value == "true" || value == "yes"
}

func maintenanceAPIClientForServer(server string) *http.Client {
	client := apiClientForServer(server)
	client.Timeout = 5 * time.Minute
	return client
}

func maintenanceAPIClient() *http.Client {
	// Drain/fill operations can legitimately take a while (provisioning or
	// destroying real resources), but a hung daemon still must not block the
	// command forever. 5 minutes matches this codebase's existing "long but
	// bounded" precedent (ADR-0004's provisioning-backoff cap) and comfortably
	// survives a serial multi-VM drain even with Hyper-V's per-VM teardown
	// wait (up to 30s each, see ADR-0004) — a shorter bound risked the CLI
	// reporting a false-negative timeout while the server-side drain was
	// still legitimately in progress.
	return &http.Client{Timeout: 5 * time.Minute}
}
