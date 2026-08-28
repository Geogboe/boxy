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
	"sort"
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

	printCurlIfEnabled(req.Context(), client, req)
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
	// A schemeless --server address defaults to https://, matching the
	// empty-string (fully-default) case above and boxy serve's HTTPS-by-
	// default posture: this is the single most natural way to point the
	// CLI at a remote server, and defaulting it to plain HTTP would build a
	// request that simply can't connect to a TLS-only listener. `boxy serve
	// --insecure` genuinely serves plain HTTP, so that case requires an
	// explicit http:// prefix here — see credentials.normalizeServerURL,
	// which must default identically or a bare --server address would
	// resolve to a different keyring key on login vs. on lookup.
	return "https://" + server
}

func defaultAPIClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func apiClientForServer(server string) *http.Client {
	client, err := apiClientForSettings(server, os.Getenv("BOXY_CA_CERT"), apiInsecureFromEnvironment())
	if err != nil {
		// Most command paths use this helper from a RunE that already has a
		// useful output channel, but the helper's historical signature cannot
		// return an error. Preserve that contract without ever returning a nil
		// client (which would turn a bad BOXY_CA_CERT path into a panic).
		return &http.Client{
			Transport: errorRoundTripper{err: err},
			Timeout:   defaultAPIClient().Timeout,
		}
	}
	return client
}

type errorRoundTripper struct{ err error }

func (t errorRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, t.err
}

func apiClientForCommand(cmd *cobra.Command, server string) (*http.Client, error) {
	caCertPath, _ := cmd.Flags().GetString("ca-cert")
	if flag := cmd.Flags().Lookup("ca-cert"); flag == nil || !flag.Changed {
		caCertPath = os.Getenv("BOXY_CA_CERT")
	}
	insecure := apiInsecureFromEnvironment()
	if flag := cmd.Flags().Lookup("insecure"); flag != nil && flag.Changed {
		insecure, _ = cmd.Flags().GetBool("insecure")
	}
	return apiClientForSettings(server, caCertPath, insecure)
}

func apiClientForSettings(server, caCertPath string, insecure bool) (*http.Client, error) {
	creds := credentials.New()
	key, _ := creds.Get(apiBaseURL(server))
	caPEM, _ := creds.GetCA(apiBaseURL(server))
	if caCertPath != "" {
		data, err := os.ReadFile(caCertPath) //nolint:gosec // the path is explicitly supplied by --ca-cert or BOXY_CA_CERT.
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

func execAPIClientForServer(server string) *http.Client {
	client := apiClientForServer(server)
	client.Timeout = 5 * time.Minute
	return client
}

// printCurlContextKey is the context key carrying whether --print-curl was
// set. It's threaded via context.Context rather than a package-level
// variable or an opts struct because the request-building helpers in this
// file (doJSON, doNoContent, and the two commands with hand-rolled
// *http.Request call sites: sandbox_exec.go's --stream path and
// status.go's checkHealth) are shared across every REST-backed command and
// don't otherwise carry per-command opts; every one of them already accepts
// or derives a context.Context, so this reuses a vehicle already present at
// every call site instead of adding a new one.
type printCurlContextKey struct{}

// withPrintCurl returns ctx annotated with whether the current command
// invocation should print the curl equivalent of each REST request it
// makes. Set once, in root.go's PersistentPreRunE, from the --print-curl
// flag.
func withPrintCurl(ctx context.Context, enabled bool) context.Context {
	if !enabled {
		return ctx
	}
	return context.WithValue(ctx, printCurlContextKey{}, true)
}

func printCurlEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(printCurlContextKey{}).(bool)
	return enabled
}

// printCurlIfEnabled writes a curl(1) command line equivalent to req to
// stderr when --print-curl is set, mirroring setupLogging's existing
// default of writing diagnostic/debug output to stderr rather than
// interleaving it with a command's normal stdout output. It is a no-op
// otherwise, and never returns an error -- printing the curl form is a
// best-effort debugging aid, not something a command's success should ever
// depend on or be blocked by.
func printCurlIfEnabled(ctx context.Context, client *http.Client, req *http.Request) {
	if !printCurlEnabled(ctx) {
		return
	}
	line, err := buildCurlCommand(client, req)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, line)
}

// redactedAuthHeader is printed in place of a real bearer token. The
// request object passed to buildCurlCommand is built before
// bearerTransport.RoundTrip adds the real Authorization header (that
// happens inside client.Do, after this function has already run), so the
// live token is never actually in scope here -- but a placeholder is
// printed anyway (rather than silently omitting the header) so the emitted
// command visibly needs an Authorization header filled in, instead of
// looking complete and then failing opaquely if copy-pasted as-is.
const redactedAuthHeader = "Authorization: Bearer <REDACTED>"

// buildCurlCommand renders req (and, for an authenticated client, a
// redacted Authorization header) as a single curl(1) command line, quoted
// for a POSIX shell. It intentionally does not attempt to reproduce this
// CLI's TLS trust configuration (--cacert, --insecure) -- that comes from
// data (a CA path, an --insecure flag) that reaches apiClientWithMaterial's
// caller, not the constructed *http.Client itself, and reconstructing it
// here would mean re-deriving it a second, easily-drifting way. A reader
// who needs TLS flags can add -k or --cacert themselves; the value this
// command is after is the method/URL/headers/body shape of the request.
func buildCurlCommand(client *http.Client, req *http.Request) (string, error) {
	var b strings.Builder
	b.WriteString("curl -X ")
	b.WriteString(req.Method)
	b.WriteString(" ")
	b.WriteString(shellQuote(req.URL.String()))

	headerNames := make([]string, 0, len(req.Header))
	for name := range req.Header {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		for _, value := range req.Header[name] {
			b.WriteString(" -H ")
			b.WriteString(shellQuote(name + ": " + value))
		}
	}
	if isBearerAuthenticated(client) {
		b.WriteString(" -H ")
		b.WriteString(shellQuote(redactedAuthHeader))
	}

	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return "", fmt.Errorf("read request body for curl equivalent: %w", err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return "", fmt.Errorf("read request body for curl equivalent: %w", err)
		}
		if len(data) > 0 {
			b.WriteString(" --data ")
			b.WriteString(shellQuote(string(data)))
		}
	}

	return b.String(), nil
}

// isBearerAuthenticated reports whether client sends requests through
// bearerTransport, i.e. whether the real (unprinted) request will carry an
// Authorization header.
func isBearerAuthenticated(client *http.Client) bool {
	if client == nil {
		return false
	}
	_, ok := client.Transport.(bearerTransport)
	return ok
}

// shellQuote wraps s in single quotes for a POSIX shell. Each embedded
// single quote is escaped using the standard idiom: close the quoted
// string, emit a backslash-escaped literal quote, then reopen the quoted
// string (see the ReplaceAll call below for the literal replacement).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func execAPIClient() *http.Client {
	// http.Client.Timeout bounds the entire request, including reading the
	// response body — for `sandbox exec --stream` that means it bounds the
	// whole live NDJSON stream, not just establishing the connection. The
	// server accepts a per-request `timeout` up to maxExecTimeout (5m, see
	// internal/server/api_exec.go) and defaults to 30s, both comfortably
	// past the 5s default client timeout; without this override, any
	// command running longer than 5s — the common case, not an edge case —
	// would have the client abort the request (or truncate the stream)
	// before the server's own bounded timeout ever had a chance to fire.
	// 5m matches the server's hard cap exactly, so this is always a safe
	// upper bound regardless of what --timeout the caller requested.
	return &http.Client{Timeout: 5 * time.Minute}
}
