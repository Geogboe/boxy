package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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

func doJSON[T any](client *http.Client, req *http.Request) (T, error) {
	var zero T

	resp, err := client.Do(req) //nolint:gosec // CLI requests intentionally target the user-configured Boxy server.
	if err != nil {
		return zero, err
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

func deleteAPI(ctx context.Context, client *http.Client, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode, nil
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

func apiBaseURL(server string) string {
	server = strings.TrimSpace(server)
	server = strings.TrimRight(server, "/")
	if server == "" {
		server = "127.0.0.1:9090"
	}
	if strings.HasPrefix(server, "http://") || strings.HasPrefix(server, "https://") {
		return server
	}
	return "http://" + server
}

func defaultAPIClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}
