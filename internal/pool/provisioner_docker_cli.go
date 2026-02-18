package pool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	boxyconfig "github.com/Geogboe/boxy/v2/internal/config"
	"github.com/Geogboe/boxy/v2/internal/model"
	"github.com/Geogboe/boxy/v2/pkg/providersdk"
	dockercfg "github.com/Geogboe/boxy/v2/pkg/providersdk/drivers/docker"
)

// DockerCLIProvisioner provisions docker containers using the local `docker` CLI.
//
// This keeps Boxy working end-to-end without pulling in additional Go SDK deps.
type DockerCLIProvisioner struct {
	Specs     map[model.PoolName]boxyconfig.PoolSpec
	Providers map[string]providersdk.Instance
	Now       func() time.Time
}

// PruneMissing removes inventory items whose backing docker container no longer exists.
//
// This is a best-effort consistency repair for cases where containers were removed
// out-of-band (e.g. `docker rm -f`) while Boxy state still referenced them.
func (p *DockerCLIProvisioner) PruneMissing(ctx context.Context, pool model.Pool) (model.Pool, error) {
	if p == nil {
		return model.Pool{}, fmt.Errorf("docker provisioner is nil")
	}
	spec, ok := p.Specs[pool.Name]
	if !ok {
		// Not a docker-backed pool (or not configured); no-op.
		return pool, nil
	}
	providerName := effectiveProviderName(spec)
	inst, ok := p.Providers[providerName]
	if !ok {
		return model.Pool{}, fmt.Errorf("pool %q references unknown provider %q", pool.Name, providerName)
	}
	dcfg, err := dockercfg.DecodeConfig(inst.Config)
	if err != nil {
		return model.Pool{}, fmt.Errorf("decode docker provider %q config: %w", inst.Name, err)
	}
	if err := dcfg.Validate(); err != nil {
		return model.Pool{}, fmt.Errorf("validate docker provider %q config: %w", inst.Name, err)
	}

	kept := make([]model.Resource, 0, len(pool.Inventory.Resources))
	for _, res := range pool.Inventory.Resources {
		id := strings.TrimSpace(string(res.ID))
		if id == "" {
			continue
		}
		_, err := dockerCmd(ctx, dcfg, "inspect", id)
		if err != nil {
			// Missing container; drop from inventory.
			continue
		}
		kept = append(kept, res)
	}
	pool.Inventory.Resources = kept
	return pool, nil
}

func (p *DockerCLIProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	if p == nil {
		return model.Resource{}, fmt.Errorf("docker provisioner is nil")
	}
	spec, ok := p.Specs[pool.Name]
	if !ok {
		return model.Resource{}, fmt.Errorf("unknown pool %q", pool.Name)
	}
	if pool.Inventory.ExpectedType == "" || pool.Inventory.ExpectedType == model.ResourceTypeUnknown {
		return model.Resource{}, fmt.Errorf("pool %q expected type is required", pool.Name)
	}
	if pool.Inventory.ExpectedProfile == "" {
		return model.Resource{}, fmt.Errorf("pool %q expected profile is required", pool.Name)
	}

	providerName := effectiveProviderName(spec)
	inst, ok := p.Providers[providerName]
	if !ok {
		return model.Resource{}, fmt.Errorf("pool %q references unknown provider %q", pool.Name, providerName)
	}
	dcfg, err := dockercfg.DecodeConfig(inst.Config)
	if err != nil {
		return model.Resource{}, fmt.Errorf("decode docker provider %q config: %w", inst.Name, err)
	}
	if err := dcfg.Validate(); err != nil {
		return model.Resource{}, fmt.Errorf("validate docker provider %q config: %w", inst.Name, err)
	}

	image, _ := getString(spec.Config, "image")
	if strings.TrimSpace(image) == "" {
		return model.Resource{}, fmt.Errorf("pool %q config.image is required", pool.Name)
	}

	command, _ := getStringSlice(spec.Config, "command")
	env, _ := getStringMap(spec.Config, "env")
	labels, _ := getStringMap(spec.Config, "labels")
	ports, _ := getStringSlice(spec.Config, "ports")
	cpu, _ := getString(spec.Config, "resources.cpu")
	memory, _ := getString(spec.Config, "resources.memory")

	// Make bash-based attacker boxes stay alive when detached.
	needsInteractive := len(command) > 0 && (command[0] == "/bin/bash" || command[0] == "bash" || command[0] == "/bin/sh" || command[0] == "sh")

	// Special-case ubuntu sshd to make examples "really work" without requiring
	// custom images.
	command = maybeBootstrapUbuntuSSHD(image, command)

	nameSuffix, err := randHex(6)
	if err != nil {
		return model.Resource{}, err
	}
	containerName := fmt.Sprintf("boxy-%s-%s", pool.Name, nameSuffix)

	runArgs := []string{"run", "--detach", "--name", containerName}
	if needsInteractive {
		runArgs = append(runArgs, "--interactive", "--tty")
	}

	mergedLabels := make(map[string]string, len(labels)+3)
	for k, v := range labels {
		mergedLabels[k] = v
	}
	mergedLabels["boxy.pool"] = string(pool.Name)
	mergedLabels["boxy.profile"] = string(pool.Inventory.ExpectedProfile)
	mergedLabels["boxy.provider"] = providerName

	labelKeys := make([]string, 0, len(mergedLabels))
	for k := range mergedLabels {
		labelKeys = append(labelKeys, k)
	}
	sort.Strings(labelKeys)
	for _, k := range labelKeys {
		runArgs = append(runArgs, "--label", k+"="+mergedLabels[k])
	}

	envKeys := make([]string, 0, len(env))
	for k := range env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		runArgs = append(runArgs, "--env", k+"="+env[k])
	}

	for _, p := range ports {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		runArgs = append(runArgs, "-p", p)
	}

	if strings.TrimSpace(cpu) != "" {
		if _, err := strconv.ParseFloat(cpu, 64); err == nil {
			runArgs = append(runArgs, "--cpus", cpu)
		}
	}
	if strings.TrimSpace(memory) != "" {
		runArgs = append(runArgs, "--memory", memory)
	}

	runArgs = append(runArgs, image)
	runArgs = append(runArgs, command...)

	out, err := dockerCmd(ctx, dcfg, runArgs...)
	if err != nil {
		return model.Resource{}, fmt.Errorf("docker run: %w", err)
	}
	containerID := strings.TrimSpace(out)
	if containerID == "" {
		return model.Resource{}, fmt.Errorf("docker run returned empty container id")
	}

	runningOut, err := dockerCmd(ctx, dcfg, "inspect", "-f", "{{.State.Running}}", containerID)
	if err != nil {
		_ = p.destroyBestEffort(ctx, dcfg, containerID)
		return model.Resource{}, fmt.Errorf("docker inspect: %w", err)
	}
	if strings.TrimSpace(runningOut) != "true" {
		logs, _ := dockerCmd(ctx, dcfg, "logs", "--tail", "80", containerID)
		_ = p.destroyBestEffort(ctx, dcfg, containerID)
		return model.Resource{}, fmt.Errorf("container %s is not running; logs:\n%s", containerID, strings.TrimSpace(logs))
	}

	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}

	return model.Resource{
		ID:       model.ResourceID(containerID),
		Type:     model.ResourceTypeContainer,
		Profile:  pool.Inventory.ExpectedProfile,
		Provider: model.ProviderRef{Name: providerName},
		State:    model.ResourceStateReady,
		Properties: map[string]any{
			"container_id":   containerID,
			"container_name": containerName,
			"image":          image,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (p *DockerCLIProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	_ = pool
	if p == nil {
		return fmt.Errorf("docker provisioner is nil")
	}
	spec, ok := p.Specs[pool.Name]
	if !ok {
		return fmt.Errorf("unknown pool %q", pool.Name)
	}
	providerName := effectiveProviderName(spec)
	inst, ok := p.Providers[providerName]
	if !ok {
		return fmt.Errorf("pool %q references unknown provider %q", pool.Name, providerName)
	}
	dcfg, err := dockercfg.DecodeConfig(inst.Config)
	if err != nil {
		return fmt.Errorf("decode docker provider %q config: %w", inst.Name, err)
	}

	id := strings.TrimSpace(string(res.ID))
	if id == "" {
		return fmt.Errorf("resource id is required")
	}
	_, err = dockerCmd(ctx, dcfg, "rm", "-f", id)
	if err != nil {
		return fmt.Errorf("docker rm -f %s: %w", id, err)
	}
	return nil
}

func (p *DockerCLIProvisioner) destroyBestEffort(ctx context.Context, cfg dockercfg.Config, id string) error {
	_, err := dockerCmd(ctx, cfg, "rm", "-f", id)
	return err
}

func effectiveProviderName(spec boxyconfig.PoolSpec) string {
	if strings.TrimSpace(spec.Provider) != "" {
		return spec.Provider
	}
	switch strings.TrimSpace(spec.Type) {
	case "docker", "container", "":
		return "docker-local"
	default:
		return spec.Provider
	}
}

func maybeBootstrapUbuntuSSHD(image string, command []string) []string {
	if len(command) == 2 && command[0] == "/usr/sbin/sshd" && command[1] == "-D" && strings.HasPrefix(image, "ubuntu:") {
		// `ubuntu:*` images don't ship with OpenSSH server.
		return []string{"bash", "-lc", "apt-get update -y && apt-get install -y openssh-server && mkdir -p /run/sshd && /usr/sbin/sshd -D"}
	}
	return command
}

func dockerCmd(ctx context.Context, cfg dockercfg.Config, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = os.Environ()
	// Avoid forcing unix socket access via DOCKER_HOST in restricted environments.
	// Prefer the user's active docker context unless an explicit TCP endpoint is configured.
	if strings.HasPrefix(cfg.Host, "tcp://") {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+cfg.Host)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s (args=%q)", msg, strings.Join(args, " "))
	}
	return stdout.String(), nil
}

func randHex(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", fmt.Errorf("nBytes must be > 0")
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func getString(m map[string]any, path string) (string, bool) {
	if len(m) == 0 || strings.TrimSpace(path) == "" {
		return "", false
	}
	parts := strings.Split(path, ".")
	cur := any(m)
	for _, p := range parts {
		mp, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = mp[p]
		if !ok {
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	default:
		return "", false
	}
}

func getStringSlice(m map[string]any, key string) ([]string, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	switch x := v.(type) {
	case []string:
		return x, true
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			s, ok := it.(string)
			if !ok {
				continue
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func getStringMap(m map[string]any, key string) (map[string]string, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	switch x := v.(type) {
	case map[string]string:
		return x, true
	case map[string]any:
		out := make(map[string]string, len(x))
		for k, it := range x {
			if s, ok := it.(string); ok {
				out[k] = s
			}
		}
		return out, true
	default:
		return nil, false
	}
}
