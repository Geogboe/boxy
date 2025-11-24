package shell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/workspacefs"
)

func TestProvisionAndLifecycle(t *testing.T) {
	logger := logrus.New()
	base := t.TempDir()
	p := New(logger, Config{BaseDir: base, AllowedShells: []string{"bash", "sh"}})

	spec := provider.ResourceSpec{
		Type:         provider.ResourceTypeProcess,
		ProviderType: p.Name(),
		Labels: map[string]string{
			"pool_id": "pool-1",
		},
	}
	ctx := context.Background()
	res, err := p.Provision(ctx, spec)
	if err != nil {
		t.Fatalf("provision error: %v", err)
	}

	paths := filepath.Join(base, res.ID)
	if _, err := os.Stat(paths); err != nil {
		t.Fatalf("expected resource dir to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths, ".boxy-resource")); err != nil {
		t.Fatalf("expected resource meta to exist: %v", err)
	}

	status, err := p.GetStatus(ctx, res)
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	if !status.Healthy {
		t.Fatalf("expected healthy, got %v", status)
	}

	sandboxID := "sbx-1"
	now := time.Now().Add(time.Hour)
	if err := p.AllocateArtifacts(res, sandboxID, &now); err != nil {
		t.Fatalf("allocate artifacts error: %v", err)
	}

	info, err := p.GetConnectionInfo(ctx, res)
	if err != nil {
		t.Fatalf("connection info error: %v", err)
	}
	if info.ExtraFields["connect_script"] == "" {
		t.Fatalf("missing connect_script")
	}
	if _, err := os.Stat(filepath.Join(paths, ".boxy-sandbox")); err != nil {
		t.Fatalf("expected sandbox meta to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths, ".envrc")); err != nil {
		t.Fatalf("expected envrc to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths, "connect.sh")); err != nil {
		t.Fatalf("expected connect script to exist: %v", err)
	}

	if err := p.Destroy(ctx, res); err != nil {
		t.Fatalf("destroy error: %v", err)
	}
	if _, err := os.Stat(paths); !os.IsNotExist(err) {
		t.Fatalf("expected resource dir removed")
	}
}

func TestStatusFailsWhenSandboxMetaMissing(t *testing.T) {
	logger := logrus.New()
	base := t.TempDir()
	p := New(logger, Config{BaseDir: base, AllowedShells: []string{"sh"}})

	spec := provider.ResourceSpec{
		Type:         provider.ResourceTypeProcess,
		ProviderType: p.Name(),
		Labels:       map[string]string{"pool_id": "pool-1"},
	}
	ctx := context.Background()
	res, err := p.Provision(ctx, spec)
	if err != nil {
		t.Fatalf("provision error: %v", err)
	}
	now := time.Now().Add(time.Hour)
	if err := p.AllocateArtifacts(res, "sbx", &now); err != nil {
		t.Fatalf("allocate artifacts error: %v", err)
	}
	// Remove sandbox meta to trigger error.
	paths := workspacefs.Layout(base, res.ID)
	_ = os.Remove(filepath.Join(paths.RootDir, ".boxy-sandbox"))

	status, err := p.GetStatus(ctx, res)
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	if status.Healthy {
		t.Fatalf("expected unhealthy status when sandbox meta missing")
	}
}

func TestEnvAndConnectContents(t *testing.T) {
	logger := logrus.New()
	base := t.TempDir()
	p := New(logger, Config{BaseDir: base, AllowedShells: []string{"sh"}})

	spec := provider.ResourceSpec{
		Type:         provider.ResourceTypeProcess,
		ProviderType: p.Name(),
		Labels:       map[string]string{"pool_id": "pool-1"},
	}
	ctx := context.Background()
	res, _ := p.Provision(ctx, spec)
	now := time.Now().Add(time.Hour)
	if err := p.AllocateArtifacts(res, "sbx-123", &now); err != nil {
		t.Fatalf("allocate artifacts error: %v", err)
	}

	paths := workspacefs.Layout(base, res.ID)
	connectData, err := os.ReadFile(paths.ConnectScript)
	if err != nil {
		t.Fatalf("read connect script: %v", err)
	}
	if !strings.Contains(string(connectData), `BOXY_SANDBOX="sbx-123"`) {
		t.Fatalf("connect script missing sandbox export")
	}
	envData, err := os.ReadFile(paths.EnvFile)
	if err != nil {
		t.Fatalf("read envrc: %v", err)
	}
	if !strings.Contains(string(envData), "BOXY_WORKSPACE=") {
		t.Fatalf("envrc missing workspace export")
	}
}

func TestStatusRespectsMinFreeBytes(t *testing.T) {
	logger := logrus.New()
	base := t.TempDir()
	p := New(logger, Config{BaseDir: base, AllowedShells: []string{"sh"}})

	spec := provider.ResourceSpec{
		Type:         provider.ResourceTypeProcess,
		ProviderType: p.Name(),
		Labels:       map[string]string{"pool_id": "pool-1"},
	}
	ctx := context.Background()
	res, _ := p.Provision(ctx, spec)

	paths := workspacefs.Layout(base, res.ID)
	fs := workspacefs.NewStatForTest()
	if err := fs.Statfs(paths.RootDir); err != nil {
		t.Skip("statfs not available")
	}
	p.cfg.MinFreeBytes = fs.FreeBytes() + 1
	status, err := p.GetStatus(ctx, res)
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	if status.Healthy {
		t.Fatalf("expected unhealthy when min_free_bytes unmet")
	}
}

func TestPickShellFallback(t *testing.T) {
	logger := logrus.New()
	base := t.TempDir()
	p := New(logger, Config{BaseDir: base, AllowedShells: []string{"definitely_missing_shell", "sh"}})
	sh := p.pickShell()
	if sh == "" {
		t.Skip("no shell found on this system")
	}
	if runtime.GOOS != "windows" && sh != "sh" {
		// On Unix, we expect sh to be found.
		t.Fatalf("expected sh, got %s", sh)
	}
}
