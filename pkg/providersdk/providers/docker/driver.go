// Package docker provides a providersdk.Driver backed by the Docker Engine API.
package docker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// dockerClient is the minimal Docker API surface used by this driver.
// *client.Client satisfies this interface; tests inject a mock.
type dockerClient interface {
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
	ContainerExecCreate(ctx context.Context, containerID string, options container.ExecOptions) (container.ExecCreateResponse, error)
	ContainerExecAttach(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error)
	ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error)
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

// Driver implements providersdk.Driver using the Docker Engine API.
type Driver struct {
	cli dockerClient
}

// New creates a Docker driver using the given config.
func New(cfg *Config) (*Driver, error) {
	host := cfg.Host
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Driver{cli: cli}, nil
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
	needsInteractive := len(command) > 0 && isShell(command[0])
	command = maybeBootstrapUbuntuSSHD(cc.Image, command)

	suffix, err := randHex(6)
	if err != nil {
		return nil, err
	}
	containerName := fmt.Sprintf("boxy-%s", suffix)

	// Env list.
	envKeys := sortedKeys(cc.Env)
	envList := make([]string, 0, len(cc.Env))
	for _, k := range envKeys {
		envList = append(envList, k+"="+cc.Env[k])
	}

	// Port bindings.
	exposedPorts, portBindings, err := parsePortSpecs(cc.Ports)
	if err != nil {
		return nil, fmt.Errorf("parse ports: %w", err)
	}

	// Resource limits.
	var nanoCPUs int64
	if strings.TrimSpace(cc.CPU) != "" {
		if f, parseErr := strconv.ParseFloat(cc.CPU, 64); parseErr == nil {
			nanoCPUs = int64(f * 1e9) //nolint:gosec // CPU count is bounded by hardware; overflow not possible
		}
	}
	var memBytes int64
	if strings.TrimSpace(cc.Memory) != "" {
		if n, parseErr := parseMemoryBytes(cc.Memory); parseErr == nil {
			memBytes = n
		}
	}

	containerCfg := &container.Config{
		Image:        cc.Image,
		Cmd:          command,
		Env:          envList,
		Labels:       cc.Labels,
		ExposedPorts: exposedPorts,
		AttachStdin:  needsInteractive,
		OpenStdin:    needsInteractive,
		Tty:          needsInteractive,
	}

	hostCfg := &container.HostConfig{
		PortBindings: portBindings,
		Resources: container.Resources{
			NanoCPUs: nanoCPUs,
			Memory:   memBytes,
		},
	}

	resp, err := d.cli.ContainerCreate(ctx, containerCfg, hostCfg, &network.NetworkingConfig{}, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("docker ContainerCreate: %w", err)
	}
	containerID := resp.ID

	if err := d.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		_ = d.deleteBestEffort(ctx, containerID)
		return nil, fmt.Errorf("docker ContainerStart: %w", err)
	}

	// Verify running.
	info, err := d.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		_ = d.deleteBestEffort(ctx, containerID)
		return nil, fmt.Errorf("docker ContainerInspect: %w", err)
	}
	if !info.State.Running {
		logs := d.containerLogs(ctx, containerID, "80")
		_ = d.deleteBestEffort(ctx, containerID)
		return nil, fmt.Errorf("container %s is not running; logs:\n%s", containerID[:12], strings.TrimSpace(logs))
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
	info, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("docker ContainerInspect %s: %w", id, err)
	}
	return &providersdk.ResourceStatus{
		ID:    id,
		State: info.State.Status,
	}, nil
}

func (d *Driver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	switch o := op.(type) {
	case *ExecOp:
		return d.execInContainer(ctx, id, o)
	default:
		return nil, fmt.Errorf("unsupported operation type %T", op)
	}
}

func (d *Driver) execInContainer(ctx context.Context, id string, op *ExecOp) (*providersdk.Result, error) {
	execResp, err := d.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          op.Command,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("docker ContainerExecCreate %s: %w", id, err)
	}

	attach, err := d.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker ContainerExecAttach %s: %w", execResp.ID, err)
	}
	defer attach.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attach.Reader); err != nil {
		return nil, fmt.Errorf("docker exec read output: %w", err)
	}

	inspect, err := d.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return nil, fmt.Errorf("docker ContainerExecInspect %s: %w", execResp.ID, err)
	}

	return &providersdk.Result{
		Outputs: map[string]string{
			"stdout":    stdout.String(),
			"stderr":    stderr.String(),
			"exit_code": strconv.Itoa(inspect.ExitCode),
		},
	}, nil
}

func (d *Driver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	info, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("docker ContainerInspect %s: %w", id, err)
	}
	name := strings.TrimPrefix(info.Name, "/")
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
	if err := d.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("docker ContainerRemove %s: %w", id, err)
	}
	return nil
}

// ExecOp runs a command inside a container.
type ExecOp struct {
	Command []string
}

func (d *Driver) deleteBestEffort(ctx context.Context, id string) error {
	return d.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

func (d *Driver) containerLogs(ctx context.Context, id, tail string) string {
	rc, err := d.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tail,
	})
	if err != nil {
		return ""
	}
	defer rc.Close() //nolint:errcheck
	var buf bytes.Buffer
	stdcopy.StdCopy(&buf, &buf, rc) //nolint:errcheck,gosec // best-effort log capture
	return buf.String()
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

func parsePortSpecs(ports []string) (nat.PortSet, nat.PortMap, error) {
	var specs []string
	for _, p := range ports {
		p = strings.TrimSpace(p)
		if p != "" {
			specs = append(specs, p)
		}
	}
	if len(specs) == 0 {
		return nat.PortSet{}, nat.PortMap{}, nil
	}
	exposed, bindings, err := nat.ParsePortSpecs(specs)
	if err != nil {
		return nil, nil, err
	}
	return exposed, bindings, nil
}

// parseMemoryBytes converts a human-readable memory string (e.g. "512m", "2g")
// to bytes. Accepts b, k, m, g suffixes (case-insensitive).
func parseMemoryBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	multipliers := []struct {
		suffix string
		mult   int64
	}{
		{"gb", 1024 * 1024 * 1024},
		{"mb", 1024 * 1024},
		{"kb", 1024},
		{"g", 1024 * 1024 * 1024},
		{"m", 1024 * 1024},
		{"k", 1024},
		{"b", 1},
	}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			num := strings.TrimSuffix(s, m.suffix)
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("parse memory %q: %w", s, err)
			}
			return int64(f * float64(m.mult)), nil //nolint:gosec // memory sizes are bounded by hardware
		}
	}
	return strconv.ParseInt(s, 10, 64)
}

func toStringSlice(items []any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case fmt.Stringer:
			out = append(out, v.String())
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
