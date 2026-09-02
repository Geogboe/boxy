package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/spf13/cobra"
)

type sandboxExecOptions struct {
	server             string
	resourceID         string
	timeout            string
	mode               sandboxExecMode
	guestPasswordStdin bool
	scriptFile         string
	interpreter        providersdk.ScriptInterpreter
	commandText        string
	attachID           string
	detach             bool
}

type sandboxExecMode uint8

const (
	sandboxExecLive sandboxExecMode = iota
	sandboxExecEvents
	sandboxExecBuffered
)

type sandboxExecRequest struct {
	Command         []string                     `json:"command"`
	CommandText     string                       `json:"command_text,omitempty"`
	Script          *providersdk.ScriptSpec      `json:"script,omitempty"`
	ResourceID      string                       `json:"resource_id,omitempty"`
	Timeout         string                       `json:"timeout,omitempty"`
	GuestCredential *providersdk.GuestCredential `json:"guest_credential,omitempty"`
}

type sandboxExecStreamEvent struct {
	Type     string            `json:"type"`
	Stream   string            `json:"stream,omitempty"`
	Data     string            `json:"data,omitempty"`
	ExitCode *int              `json:"exit_code,omitempty"`
	Error    string            `json:"error,omitempty"`
	Attrs    map[string]string `json:"attributes,omitempty"`
}

type sandboxExecutionResponse struct {
	ExecID    string                  `json:"exec_id"`
	Status    string                  `json:"status"`
	Chunks    []sandboxExecutionChunk `json:"chunks,omitempty"`
	Next      string                  `json:"next"`
	ExitCode  *int                    `json:"exit_code,omitempty"`
	Error     string                  `json:"error,omitempty"`
	Truncated bool                    `json:"truncated,omitempty"`
}

type sandboxExecutionChunk struct {
	Cursor    string `json:"cursor"`
	Stream    string `json:"stream,omitempty"`
	Data      string `json:"data,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func newSandboxExecCommand(serverAddr func() string) *cobra.Command {
	var resourceID, timeout, scriptFile, interpreter string
	var commandText, attachID string
	var events, buffered, guestPasswordStdin, stdinCommand, detach bool
	cmd := &cobra.Command{
		Use:   "exec <id> -- <command> [args...]",
		Short: "Execute a one-shot command with live output in a ready sandbox",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("requires a sandbox id and a command after --")
			}
			if attachID != "" {
				if len(args) != 1 || commandText != "" || stdinCommand || scriptFile != "" || detach || events || buffered {
					return errors.New("--attach cannot be combined with command input or output mode flags")
				}
				return nil
			}
			inputs := 0
			if scriptFile != "" {
				inputs++
			}
			if commandText != "" {
				inputs++
			}
			if stdinCommand {
				inputs++
			}
			if scriptFile == "" && len(args) > 1 && strings.HasPrefix(args[1], "@") {
				inputs++
			} else if scriptFile == "" && len(args) > 1 {
				inputs++
			}
			if inputs > 1 {
				return errors.New("command input forms cannot be used together")
			}
			if inputs != 1 {
				return errors.New("exactly one of positional command, --command, --stdin, or --script-file is required")
			}
			if scriptFile == "" && len(args) < 2 && commandText == "" && !stdinCommand {
				return fmt.Errorf("requires a sandbox id and a command after --")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if events && buffered {
				return fmt.Errorf("--events and --buffered cannot be used together")
			}
			if stdinCommand && guestPasswordStdin {
				return errors.New("--stdin and --guest-password-stdin cannot both consume stdin")
			}
			if stdinCommand {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read command from stdin: %w", err)
				}
				if strings.TrimSpace(string(data)) == "" {
					return errors.New("--stdin command must not be blank")
				}
				commandText = string(data)
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
				scriptFile:         scriptFile,
				interpreter:        providersdk.ScriptInterpreter(interpreter),
				commandText:        commandText,
				attachID:           attachID,
				detach:             detach,
			}, id, args[1:], cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&resourceID, "resource", "", "resource ID (required for multi-resource sandboxes)")
	cmd.Flags().StringVar(&timeout, "timeout", "", "execution timeout (default 30s, maximum 5m)")
	cmd.Flags().BoolVar(&events, "events", false, "write structured NDJSON events instead of human-readable output")
	cmd.Flags().BoolVar(&buffered, "buffered", false, "wait for completion and request one buffered JSON response")
	cmd.Flags().StringVar(&commandText, "command", "", "execute one opaque command string")
	cmd.Flags().BoolVar(&stdinCommand, "stdin", false, "read one opaque command string from stdin")
	cmd.Flags().BoolVar(&detach, "detach", false, "submit the execution, print its ID, and return")
	cmd.Flags().StringVar(&attachID, "attach", "", "attach to an existing execution ID")
	cmd.Flags().BoolVar(&guestPasswordStdin, "guest-password-stdin", false, "read the guest password from stdin (never pass it as a flag value)")
	cmd.Flags().StringVar(&scriptFile, "script-file", "", "stage and execute a local script file")
	cmd.Flags().StringVar(&interpreter, "interpreter", string(providersdk.ScriptInterpreterAuto), "script interpreter: auto, powershell, or sh")
	return cmd
}

func runSandboxExec(ctx context.Context, opts sandboxExecOptions, id string, command []string, in io.Reader, out, errOut io.Writer) error {
	return runDurableSandboxExec(ctx, opts, id, command, in, out, errOut)

}

func runDurableSandboxExec(ctx context.Context, opts sandboxExecOptions, id string, command []string, in io.Reader, out, errOut io.Writer) error {
	var script *providersdk.ScriptSpec
	var err error
	if opts.attachID == "" {
		if opts.commandText == "" {
			command, script, err = parseSandboxExecPayload(command, opts.scriptFile, opts.interpreter)
			if err != nil {
				return err
			}
		} else {
			command = nil
		}
	}
	base := apiBaseURL(opts.server)
	client := execAPIClientForServer(opts.server)
	if opts.attachID == "" {
		credential, credentialErr := guestCredentialFromCLI(in, opts.guestPasswordStdin)
		if credentialErr != nil {
			return credentialErr
		}
		if credential == nil {
			credential, credentialErr = guestCredentialFromKeyring(ctx, opts.server, id, opts.resourceID)
			if credentialErr != nil {
				return credentialErr
			}
		}
		request := sandboxExecRequest{Command: command, CommandText: opts.commandText, Script: script, ResourceID: opts.resourceID, Timeout: opts.timeout, GuestCredential: credential}
		endpoint := base + "/api/v1/sandboxes/" + id + "/exec"
		accepted, postErr := postJSON[sandboxExecRequest, struct {
			ExecID string `json:"exec_id"`
			Status string `json:"status"`
		}](ctx, client, endpoint, request)
		if postErr != nil {
			return postErr
		}
		if opts.detach {
			_, _ = fmt.Fprintln(out, accepted.ExecID)
			return nil
		}
		return tailSandboxExecution(ctx, client, base, id, accepted.ExecID, opts.mode, out, errOut)
	}
	return tailSandboxExecution(ctx, client, base, id, opts.attachID, opts.mode, out, errOut)
}

func tailSandboxExecution(ctx context.Context, client *http.Client, base, sandboxID, executionID string, mode sandboxExecMode, out, errOut io.Writer) error {
	cursor := ""
	var bufferedStdout, bufferedStderr bytes.Buffer
	for {
		endpoint := base + "/api/v1/sandboxes/" + sandboxID + "/exec/" + executionID
		if cursor != "" {
			endpoint += "?from=" + url.QueryEscape(cursor)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		printCurlIfEnabled(ctx, client, request)
		response, err := client.Do(request) //nolint:gosec // endpoint comes from the user-selected Boxy server.
		if err != nil {
			if ctx.Err() != nil {
				_ = cancelSandboxExecution(context.Background(), client, base, sandboxID, executionID)
			}
			return wrapConnError(err, request.URL.Host)
		}
		var page sandboxExecutionResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			if decodeErr != nil {
				return fmt.Errorf("read execution response: %w", decodeErr)
			}
			return fmt.Errorf("execution request returned HTTP %d", response.StatusCode)
		}
		if decodeErr != nil {
			return fmt.Errorf("decode execution status: %w", decodeErr)
		}
		for _, chunk := range page.Chunks {
			if chunk.Truncated {
				if mode == sandboxExecEvents {
					_, _ = fmt.Fprintf(out, "{\"type\":\"truncated\",\"cursor\":%q}\n", chunk.Cursor)
				}
				continue
			}
			data, decodeErr := base64.StdEncoding.DecodeString(chunk.Data)
			if decodeErr != nil {
				return fmt.Errorf("decode execution chunk: %w", decodeErr)
			}
			switch {
			case mode == sandboxExecEvents:
				event := sandboxExecStreamEvent{Type: "data", Stream: chunk.Stream, Data: chunk.Data}
				if err := json.NewEncoder(out).Encode(event); err != nil {
					return fmt.Errorf("write execution event: %w", err)
				}
			case mode == sandboxExecBuffered:
				if chunk.Stream == "stderr" {
					_, _ = bufferedStderr.Write(data)
				} else {
					_, _ = bufferedStdout.Write(data)
				}
			case chunk.Stream == "stderr":
				_, _ = errOut.Write(data)
			default:
				_, _ = out.Write(data)
			}
		}
		if page.Next != "" {
			cursor = page.Next
		}
		if page.Status == "running" || page.Status == "pending" {
			select {
			case <-ctx.Done():
				_ = cancelSandboxExecution(context.Background(), client, base, sandboxID, executionID)
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		if mode == sandboxExecEvents {
			terminal := sandboxExecStreamEvent{Type: "complete", Error: page.Error, Attrs: map[string]string{"status": page.Status}}
			if page.ExitCode != nil {
				terminal.ExitCode = page.ExitCode
				terminal.Attrs["exit_code"] = strconv.Itoa(*page.ExitCode)
			}
			if err := json.NewEncoder(out).Encode(terminal); err != nil {
				return fmt.Errorf("write execution completion: %w", err)
			}
		}
		if mode == sandboxExecBuffered {
			if _, err := out.Write(bufferedStdout.Bytes()); err != nil {
				return fmt.Errorf("write buffered stdout: %w", err)
			}
			if _, err := errOut.Write(bufferedStderr.Bytes()); err != nil {
				return fmt.Errorf("write buffered stderr: %w", err)
			}
		}
		if page.ExitCode != nil && *page.ExitCode != 0 {
			return NewExitCodeError(*page.ExitCode)
		}
		if page.Error != "" {
			return errors.New(page.Error)
		}
		if page.Status == "cancelled" || page.Status == "interrupted" {
			return fmt.Errorf("execution %s", page.Status)
		}
		return nil
	}
}

func cancelSandboxExecution(ctx context.Context, client *http.Client, base, sandboxID, executionID string) error {
	endpoint := base + "/api/v1/sandboxes/" + sandboxID + "/exec/" + executionID + "/cancel"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request) //nolint:gosec // endpoint comes from the user-selected Boxy server.
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	return nil
}

func parseSandboxExecPayload(command []string, scriptFile string, interpreter providersdk.ScriptInterpreter) ([]string, *providersdk.ScriptSpec, error) {
	if interpreter == "" {
		interpreter = providersdk.ScriptInterpreterAuto
	}
	if scriptFile != "" && len(command) > 0 && strings.HasPrefix(command[0], "@") {
		return nil, nil, errors.New("--script-file and @script-file cannot be used together")
	}
	if scriptFile == "" && len(command) > 0 && strings.HasPrefix(command[0], "@") {
		scriptFile = strings.TrimPrefix(command[0], "@")
		command = command[1:]
	}
	if scriptFile != "" {
		if interpreter != providersdk.ScriptInterpreterAuto && interpreter != providersdk.ScriptInterpreterPowerShell && interpreter != providersdk.ScriptInterpreterSH {
			return nil, nil, fmt.Errorf("unsupported script interpreter %q", interpreter)
		}
		content, err := readScriptFile(scriptFile)
		if err != nil {
			return nil, nil, err
		}
		digest := sha256.Sum256(content)
		return nil, &providersdk.ScriptSpec{
			Content: content, Digest: fmt.Sprintf("%x", digest[:]), Interpreter: interpreter, Args: append([]string(nil), command...),
		}, nil
	}
	if interpreter != providersdk.ScriptInterpreterAuto {
		return nil, nil, errors.New("--interpreter is only valid with a script file")
	}
	if len(command) == 0 {
		return nil, nil, errors.New("requires a sandbox id and a command after --")
	}
	return command, nil, nil
}

func readScriptFile(name string) ([]byte, error) {
	path := filepath.Clean(name)
	if path == "." || path == "" {
		return nil, errors.New("script file path is required")
	}
	f, err := os.Open(path) //nolint:gosec // the operator explicitly selected this local script.
	if err != nil {
		return nil, fmt.Errorf("open script file %q: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(io.LimitReader(f, providersdk.MaxScriptBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read script file %q: %w", name, err)
	}
	if len(content) > providersdk.MaxScriptBytes {
		return nil, fmt.Errorf("script file %q exceeds the %d MiB limit", name, providersdk.MaxScriptBytes>>20)
	}
	return content, nil
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
