package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/spf13/cobra"
)

type sandboxExecOptions struct {
	server             string
	resourceID         string
	timeout            string
	mode               sandboxExecMode
	guestPasswordStdin bool
}

type sandboxExecMode uint8

const (
	sandboxExecLive sandboxExecMode = iota
	sandboxExecEvents
	sandboxExecBuffered
)

type sandboxExecRequest struct {
	Command         []string                     `json:"command"`
	ResourceID      string                       `json:"resource_id,omitempty"`
	Timeout         string                       `json:"timeout,omitempty"`
	GuestCredential *providersdk.GuestCredential `json:"guest_credential,omitempty"`
}

type sandboxExecResponse struct {
	ResourceID string `json:"resource_id"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code"`
}

type sandboxExecStreamEvent struct {
	Type     string            `json:"type"`
	Stream   string            `json:"stream,omitempty"`
	Data     string            `json:"data,omitempty"`
	ExitCode *int              `json:"exit_code,omitempty"`
	Error    string            `json:"error,omitempty"`
	Attrs    map[string]string `json:"attributes,omitempty"`
}

func newSandboxExecCommand(serverAddr func() string) *cobra.Command {
	var resourceID, timeout string
	var events, buffered, guestPasswordStdin bool
	cmd := &cobra.Command{
		Use:   "exec <id> -- <command> [args...]",
		Short: "Execute a one-shot command with live output in a ready sandbox",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("requires a sandbox id and a command after --")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if events && buffered {
				return fmt.Errorf("--events and --buffered cannot be used together")
			}
			id, err := validatePathID("sandbox id", args[0])
			if err != nil {
				return err
			}
			mode := sandboxExecLive
			if events {
				mode = sandboxExecEvents
			} else if buffered {
				mode = sandboxExecBuffered
			}
			return runSandboxExec(cmd.Context(), sandboxExecOptions{
				server:             serverAddr(),
				resourceID:         resourceID,
				timeout:            timeout,
				mode:               mode,
				guestPasswordStdin: guestPasswordStdin,
			}, id, args[1:], cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&resourceID, "resource", "", "resource ID (required for multi-resource sandboxes)")
	cmd.Flags().StringVar(&timeout, "timeout", "", "execution timeout (default 30s, maximum 5m)")
	cmd.Flags().BoolVar(&events, "events", false, "write structured NDJSON events instead of human-readable output")
	cmd.Flags().BoolVar(&buffered, "buffered", false, "wait for completion and request one buffered JSON response")
	cmd.Flags().BoolVar(&guestPasswordStdin, "guest-password-stdin", false, "read the guest password from stdin (never pass it as a flag value)")
	return cmd
}

func runSandboxExec(ctx context.Context, opts sandboxExecOptions, id string, command []string, in io.Reader, out, errOut io.Writer) error {
	base := apiBaseURL(opts.server)
	credential, err := guestCredentialFromCLI(in, opts.guestPasswordStdin)
	if err != nil {
		return err
	}
	if credential == nil {
		credential, err = guestCredentialFromKeyring(ctx, opts.server, id, opts.resourceID)
		if err != nil {
			return err
		}
	}
	request := sandboxExecRequest{Command: command, ResourceID: opts.resourceID, Timeout: opts.timeout, GuestCredential: credential}
	// The default client's 5s http.Client.Timeout bounds the whole request
	// (including reading a streaming response body), but the server accepts
	// exec timeouts up to 5 minutes — see execAPIClientForServer's doc
	// comment.
	client := execAPIClientForServer(opts.server)
	if opts.mode == sandboxExecBuffered {
		endpoint, err := url.Parse(base + "/api/v1/sandboxes/" + id + "/exec")
		if err != nil {
			return err
		}
		query := endpoint.Query()
		query.Set("stream", "false")
		endpoint.RawQuery = query.Encode()
		response, err := postJSON[sandboxExecRequest, sandboxExecResponse](ctx, client, endpoint.String(), request)
		if err != nil {
			return err
		}
		if response.Stdout != "" {
			_, _ = io.WriteString(out, response.Stdout)
		}
		if response.Stderr != "" {
			_, _ = io.WriteString(errOut, response.Stderr)
		}
		if response.ExitCode != 0 {
			return NewExitCodeError(response.ExitCode)
		}
		return nil
	}

	body := new(bytes.Buffer)
	if err := json.NewEncoder(body).Encode(request); err != nil {
		return err
	}
	endpoint, err := url.Parse(base + "/api/v1/sandboxes/" + id + "/exec")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	printCurlIfEnabled(ctx, client, req)
	resp, err := client.Do(req) //nolint:gosec // endpoint comes from the user-selected Boxy server.
	if err != nil {
		return wrapConnError(err, req.URL.Host)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(resp, req.URL.String())
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	completed := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if opts.mode == sandboxExecEvents {
			if _, err := fmt.Fprintln(out, string(line)); err != nil {
				return fmt.Errorf("write exec stream event: %w", err)
			}
		}
		var event sandboxExecStreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("decode exec stream event: %w", err)
		}
		switch event.Type {
		case "data":
			if opts.mode == sandboxExecEvents {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(event.Data)
			if err != nil {
				return fmt.Errorf("decode exec stream data: %w", err)
			}
			if event.Stream == "stderr" {
				_, _ = errOut.Write(data)
			} else {
				_, _ = out.Write(data)
			}
		case "complete":
			completed = true
			if event.Error != "" {
				return fmt.Errorf("command stream failed: %s", event.Error)
			}
			if event.ExitCode == nil && event.Attrs != nil {
				if raw := event.Attrs["exit_code"]; raw != "" {
					code, parseErr := strconv.Atoi(raw)
					if parseErr == nil {
						event.ExitCode = &code
					}
				}
			}
			if event.ExitCode != nil && *event.ExitCode != 0 {
				return NewExitCodeError(*event.ExitCode)
			}
		default:
			return fmt.Errorf("unknown exec stream event type %q", event.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read exec stream: %w", err)
	}
	if !completed {
		return fmt.Errorf("exec stream ended before the complete event")
	}
	return nil
}

func guestCredentialFromCLI(in io.Reader, readStdin bool) (*providersdk.GuestCredential, error) {
	password, fromEnv := os.LookupEnv("BOXY_GUEST_PASSWORD")
	if readStdin {
		if fromEnv {
			return nil, fmt.Errorf("use either --guest-password-stdin or BOXY_GUEST_PASSWORD, not both")
		}
		data, err := io.ReadAll(in)
		if err != nil {
			return nil, fmt.Errorf("read guest password from stdin: %w", err)
		}
		password = string(data)
		fromEnv = true
	}
	if !fromEnv || strings.TrimSpace(password) == "" {
		return nil, nil
	}
	data, err := json.Marshal(map[string]string{"password": strings.TrimSpace(password)})
	if err != nil {
		return nil, fmt.Errorf("encode guest credential: %w", err)
	}
	return &providersdk.GuestCredential{Kind: "password", Data: data}, nil
}
