package docker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// Driver implements providersdk.Driver using the local docker CLI.
type Driver struct {
	host string
}

// New creates a Docker driver with the given config.
func New(cfg *Config) *Driver {
	host := cfg.Host
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	return &Driver{host: host}
}

func (d *Driver) Type() providersdk.Type { return ProviderType }

func (d *Driver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	cc, err := decodeCreateConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("decode create config: %w", err)
	}
	if strings.TrimSpace(cc.Image) == "" {
		return nil, fmt.Errorf("config.image is required")
	}

	command := toStringSlice(cc.Command)

	// Make shell-based containers stay alive when detached.
	needsInteractive := len(command) > 0 && isShell(command[0])

	// Special-case ubuntu sshd.
	command = maybeBootstrapUbuntuSSHD(cc.Image, command)

	suffix, err := randHex(6)
	if err != nil {
		return nil, err
	}
	containerName := fmt.Sprintf("boxy-%s", suffix)

	runArgs := []string{"run", "--detach", "--name", containerName}
	if needsInteractive {
		runArgs = append(runArgs, "--interactive", "--tty")
	}

	// Labels.
	labelKeys := sortedKeys(cc.Labels)
	for _, k := range labelKeys {
		runArgs = append(runArgs, "--label", k+"="+cc.Labels[k])
	}

	// Env.
	envKeys := sortedKeys(cc.Env)
	for _, k := range envKeys {
		runArgs = append(runArgs, "--env", k+"="+cc.Env[k])
	}

	// Ports.
	for _, p := range cc.Ports {
		p = strings.TrimSpace(p)
		if p != "" {
			runArgs = append(runArgs, "-p", p)
		}
	}

	// Resource limits.
	if strings.TrimSpace(cc.CPU) != "" {
		if _, err := strconv.ParseFloat(cc.CPU, 64); err == nil {
			runArgs = append(runArgs, "--cpus", cc.CPU)
		}
	}
	if strings.TrimSpace(cc.Memory) != "" {
		runArgs = append(runArgs, "--memory", cc.Memory)
	}

	runArgs = append(runArgs, cc.Image)
	runArgs = append(runArgs, command...)

	out, err := d.dockerCmd(ctx, runArgs...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	containerID := strings.TrimSpace(out)
	if containerID == "" {
		return nil, fmt.Errorf("docker run returned empty container id")
	}

	// Verify running.
	runningOut, err := d.dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", containerID)
	if err != nil {
		_ = d.deleteBestEffort(ctx, containerID)
		return nil, fmt.Errorf("docker inspect: %w", err)
	}
	if strings.TrimSpace(runningOut) != "true" {
		logs, _ := d.dockerCmd(ctx, "logs", "--tail", "80", containerID)
		_ = d.deleteBestEffort(ctx, containerID)
		return nil, fmt.Errorf("container %s is not running; logs:\n%s", containerID, strings.TrimSpace(logs))
	}

	return &providersdk.Resource{
		ID: containerID,
		ConnectionInfo: map[string]string{
			"container_id":   containerID,
			"container_name": containerName,
			"image":          cc.Image,
		},
	}, nil
}

func (d *Driver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	out, err := d.dockerCmd(ctx, "inspect", "-f", "{{.State.Status}}", id)
	if err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", id, err)
	}
	return &providersdk.ResourceStatus{
		ID:    id,
		State: strings.TrimSpace(out),
	}, nil
}

func (d *Driver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	switch o := op.(type) {
	case *ExecOp:
		args := append([]string{"exec", id}, o.Command...)
		out, err := d.dockerCmd(ctx, args...)
		if err != nil {
			return nil, fmt.Errorf("docker exec %s: %w", id, err)
		}
		return &providersdk.Result{
			Outputs: map[string]string{"stdout": out},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported operation type %T", op)
	}
}

func (d *Driver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	out, err := d.dockerCmd(ctx, "inspect", "-f", "{{.Name}}", id)
	if err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", id, err)
	}
	name := strings.TrimPrefix(strings.TrimSpace(out), "/")
	if name == "" {
		name = id
	}
	return map[string]any{
		"access": "docker-exec",
		"exec":   fmt.Sprintf("docker exec -it %s /bin/sh", name),
	}, nil
}

func (d *Driver) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("resource id is required")
	}
	_, err := d.dockerCmd(ctx, "rm", "-f", id)
	if err != nil {
		return fmt.Errorf("docker rm -f %s: %w", id, err)
	}
	return nil
}

// ExecOp runs a command inside a container.
type ExecOp struct {
	Command []string
}

func (d *Driver) deleteBestEffort(ctx context.Context, id string) error {
	_, err := d.dockerCmd(ctx, "rm", "-f", id)
	return err
}

func (d *Driver) dockerCmd(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = os.Environ()
	if strings.HasPrefix(d.host, "tcp://") {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+d.host)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s (args=%q)", friendlyDockerErr(err, msg), strings.Join(args, " "))
	}
	return stdout.String(), nil
}

// friendlyDockerErr translates low-level Docker exec errors into actionable messages.
func friendlyDockerErr(err error, stderr string) string {
	// Binary not on PATH — Docker Desktop isn't running or was never installed.
	if errors.Is(err, exec.ErrNotFound) {
		return "docker CLI not found in PATH — is Docker Desktop running? On Windows/WSL, Docker Desktop must be started before use"
	}
	// Daemon unreachable — CLI exists but the daemon socket/pipe isn't open.
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "cannot connect to the docker daemon") ||
		strings.Contains(lower, "error during connect") ||
		strings.Contains(lower, "the system cannot find the file specified") && strings.Contains(lower, "//./pipe/docker") {
		return "cannot connect to Docker daemon — start Docker Desktop (or 'sudo systemctl start docker' on Linux): " + stderr
	}
	return stderr
}

func decodeCreateConfig(cfg any) (CreateConfig, error) {
	switch v := cfg.(type) {
	case map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return CreateConfig{}, err
		}
		var cc CreateConfig
		if err := json.Unmarshal(b, &cc); err != nil {
			return CreateConfig{}, err
		}
		return cc, nil
	case *CreateConfig:
		return *v, nil
	case CreateConfig:
		return v, nil
	default:
		return CreateConfig{}, fmt.Errorf("unexpected config type %T", cfg)
	}
}

func toStringSlice(items []any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		} else if s, ok := item.(fmt.Stringer); ok {
			out = append(out, s.String())
		}
	}
	return out
}

func isShell(cmd string) bool {
	return cmd == "/bin/bash" || cmd == "bash" || cmd == "/bin/sh" || cmd == "sh"
}

func maybeBootstrapUbuntuSSHD(image string, command []string) []string {
	if len(command) == 2 && command[0] == "/usr/sbin/sshd" && command[1] == "-D" && strings.HasPrefix(image, "ubuntu:") {
		return []string{"bash", "-lc", "apt-get update -y && apt-get install -y openssh-server && mkdir -p /run/sshd && /usr/sbin/sshd -D"}
	}
	return command
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
