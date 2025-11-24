package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/workspacefs"
)

// Config drives the scratch shell provider.
type Config struct {
	BaseDir       string   // root under which resources are created
	AllowedShells []string // order of preference, e.g., ["bash", "zsh", "sh"]
	MinFreeBytes  uint64   // optional free-space check
}

// Provider implements scratch shell workspaces.
type Provider struct {
	cfg    Config
	logger *logrus.Logger
}

// New creates a scratch shell provider.
func New(logger *logrus.Logger, cfg Config) *Provider {
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(os.TempDir(), "boxy-scratch")
	}
	if len(cfg.AllowedShells) == 0 {
		cfg.AllowedShells = []string{"bash", "sh"}
	}
	return &Provider{cfg: cfg, logger: logger}
}

func (p *Provider) Provision(ctx context.Context, spec provider.ResourceSpec) (*provider.Resource, error) {
	res := provider.NewResource(spec.Labels["pool_id"], provider.ResourceTypeProcess, p.Name())

	paths, err := workspacefs.Provision(p.cfg.BaseDir, res.ID)
	if err != nil {
		return nil, err
	}

	resMeta := resourceMeta{
		ResourceID: res.ID,
		PoolID:     res.PoolID,
		CreatedAt:  time.Now().UTC(),
	}
	resMetaPath := filepath.Join(paths.RootDir, ".boxy-resource")
	if err := workspacefs.WriteJSONFile(resMetaPath, resMeta); err != nil {
		return nil, fmt.Errorf("write resource meta: %w", err)
	}

	res.ProviderID = paths.RootDir
	res.State = provider.StateReady
	res.Spec = map[string]interface{}{
		"base_dir": p.cfg.BaseDir,
	}
	res.Metadata = map[string]interface{}{
		"workspace_dir": paths.WorkspaceDir,
		"connect":       paths.ConnectScript,
		"resource_meta": resMetaPath,
	}
	return res, nil
}

func (p *Provider) Destroy(ctx context.Context, res *provider.Resource) error {
	paths := workspacefs.Layout(p.cfg.BaseDir, res.ID)
	return workspacefs.Cleanup(paths)
}

func (p *Provider) GetStatus(ctx context.Context, res *provider.Resource) (*provider.ResourceStatus, error) {
	paths := workspacefs.Layout(p.cfg.BaseDir, res.ID)
	required := []string{}
	if metaPath, ok := res.Metadata["resource_meta"].(string); ok && metaPath != "" {
		required = append(required, metaPath)
	}
	if sbMetaPath, ok := res.Metadata["sandbox_meta"].(string); ok && sbMetaPath != "" {
		required = append(required, sbMetaPath)
	}
	err := workspacefs.HealthCheck(paths, required, p.cfg.MinFreeBytes)
	healthy := err == nil
	state := provider.StateReady
	message := "ok"
	if err != nil {
		state = provider.StateError
		message = err.Error()
	}
	return &provider.ResourceStatus{
		State:     state,
		Healthy:   healthy,
		Message:   message,
		LastCheck: time.Now(),
	}, nil
}

func (p *Provider) GetConnectionInfo(ctx context.Context, res *provider.Resource) (*provider.ConnectionInfo, error) {
	paths := workspacefs.Layout(p.cfg.BaseDir, res.ID)
	return &provider.ConnectionInfo{
		Type: "shell",
		ExtraFields: map[string]interface{}{
			"connect_script": paths.ConnectScript,
			"workspace_dir":  paths.WorkspaceDir,
		},
	}, nil
}

func (p *Provider) Update(ctx context.Context, res *provider.Resource, updates provider.ResourceUpdate) error {
	return errors.New("scratch/shell: update not supported")
}

func (p *Provider) Exec(ctx context.Context, res *provider.Resource, cmd []string) (*provider.ExecResult, error) {
	return nil, errors.New("scratch/shell: exec not supported")
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	// Simple check: ensure base dir is writable.
	if err := os.MkdirAll(p.cfg.BaseDir, 0o755); err != nil {
		return fmt.Errorf("ensure base dir: %w", err)
	}
	return nil
}

func (p *Provider) Name() string {
	return "scratch/shell"
}

func (p *Provider) Type() provider.ResourceType {
	return provider.ResourceTypeProcess
}

// AllocateArtifacts writes sandbox metadata and connect scripts.
// Intended to be called by the pool manager when allocating to a sandbox.
func (p *Provider) AllocateArtifacts(res *provider.Resource, sandboxID string, expiresAt *time.Time) error {
	paths := workspacefs.Layout(p.cfg.BaseDir, res.ID)
	shell := p.pickShell()
	if shell == "" {
		return fmt.Errorf("no allowed shell found")
	}
	connect := buildConnectScript(paths.WorkspaceDir, sandboxID, shell)
	env := fmt.Sprintf("BOXY_SANDBOX=%s\nBOXY_WORKSPACE=%s\n", sandboxID, paths.WorkspaceDir)
	if err := os.WriteFile(paths.ConnectScript, []byte(connect), 0o755); err != nil {
		return fmt.Errorf("write connect script: %w", err)
	}
	if err := os.WriteFile(paths.EnvFile, []byte(env), 0o644); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}
	sbMeta := sandboxMeta{
		SandboxID:   sandboxID,
		AllocatedAt: time.Now().UTC(),
		ExpiresAt:   expiresAt,
	}
	sbMetaPath := filepath.Join(paths.RootDir, ".boxy-sandbox")
	if err := workspacefs.WriteJSONFile(sbMetaPath, sbMeta); err != nil {
		return fmt.Errorf("write sandbox meta: %w", err)
	}
	// Record sandbox metadata path for later checks.
	if res.Metadata == nil {
		res.Metadata = map[string]interface{}{}
	}
	res.Metadata["sandbox_meta"] = sbMetaPath
	return nil
}

func (p *Provider) pickShell() string {
	for _, sh := range p.cfg.AllowedShells {
		if _, err := exec.LookPath(sh); err == nil {
			return sh
		}
	}
	return ""
}

func buildConnectScript(workspaceDir, sandboxID, shell string) string {
	return fmt.Sprintf(`#!/bin/sh
cd "%s" || exit 1
export BOXY_SANDBOX="%s"
export BOXY_WORKSPACE="%s"
export PS1="(boxy:%s) \w $ "
exec %s --noprofile --norc
`, workspaceDir, sandboxID, workspaceDir, sandboxID, shell)
}

var _ provider.Provider = (*Provider)(nil)

type resourceMeta struct {
	ResourceID string    `json:"resource_id"`
	PoolID     string    `json:"pool_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type sandboxMeta struct {
	SandboxID   string     `json:"sandbox_id"`
	AllocatedAt time.Time  `json:"allocated_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}
