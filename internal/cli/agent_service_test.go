// internal/cli/agent_service_test.go
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/spf13/cobra"
)

// fakeManager stubs svcmgr.Manager for command-layer tests.
type fakeManager struct {
	installedSpecs []svcmgr.Spec
	installErr     error
	uninstallErr   error
	startErr       error
	stopErr        error
	status         svcmgr.Status
	statusErr      error
}

func (f *fakeManager) Install(spec svcmgr.Spec) error {
	if f.installErr != nil {
		return f.installErr
	}
	f.installedSpecs = append(f.installedSpecs, spec)
	return nil
}
func (f *fakeManager) Uninstall(string) error               { return f.uninstallErr }
func (f *fakeManager) Start(string) error                   { return f.startErr }
func (f *fakeManager) Stop(string) error                    { return f.stopErr }
func (f *fakeManager) Status(string) (svcmgr.Status, error) { return f.status, f.statusErr }

func withFakeSvcManager(t *testing.T, m *fakeManager) {
	t.Helper()
	orig := svcmgrNewManager
	svcmgrNewManager = func(svcmgr.ManagerOptions) (svcmgr.Manager, error) { return m, nil }
	t.Cleanup(func() { svcmgrNewManager = orig })
}

func withElevated(t *testing.T, elevated bool) {
	t.Helper()
	orig := isElevatedFn
	isElevatedFn = func() (bool, error) { return elevated, nil }
	t.Cleanup(func() { isElevatedFn = orig })
}

func newTestCmd(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd
}

func TestAgentServiceInstall_Elevated_WritesConfigAndInstalls(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".boxy-agent")

	var out bytes.Buffer
	err := runAgentServiceInstall(newTestCmd(&out), agentServiceInstallOpts{
		userMode: false,
		agentOpts: agentServeOpts{
			server:    "boxy-server:9091",
			providers: []string{"docker"},
			dataDir:   dataDir,
		},
	})
	if err != nil {
		t.Fatalf("runAgentServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 {
		t.Fatalf("expected exactly one Install call, got %d", len(m.installedSpecs))
	}
	spec := m.installedSpecs[0]
	if spec.Name != "boxy-agent" {
		t.Fatalf("Spec.Name = %q, want boxy-agent", spec.Name)
	}

	cfgPath := filepath.Join(dataDir, "service.yaml")
	cfg, err := loadAgentServiceConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadAgentServiceConfig: %v", err)
	}
	if cfg.Server != "boxy-server:9091" || !filepath.IsAbs(cfg.DataDir) {
		t.Fatalf("saved config = %+v, unexpected", cfg)
	}

	wantArgs := []string{"agent", "serve", "--service-config", cfgPath, "--log-file", cfg.LogFile}
	if !slices.Equal(spec.Args, wantArgs) {
		t.Fatalf("Spec.Args = %v, want %v (log file must be a literal arg — root's --log-file persistent flag is only read off the cmdline, not from the service config)", spec.Args, wantArgs)
	}
}

func TestAgentServiceInstall_NotElevated_ErrorsWithoutInstalling(t *testing.T) {
	withElevated(t, false)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		userMode:  false,
		agentOpts: agentServeOpts{server: "s:9091", providers: []string{"docker"}, dataDir: t.TempDir()},
	})
	if err == nil {
		t.Fatal("expected an error when not elevated and --user was not passed")
	}
	if len(m.installedSpecs) != 0 {
		t.Fatal("must not call Install when the elevation check fails")
	}
}

func TestAgentServiceInstall_UserMode_SkipsElevationCheck(t *testing.T) {
	withElevated(t, false)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		userMode:  true,
		agentOpts: agentServeOpts{server: "s:9091", providers: []string{"docker"}, dataDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("runAgentServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 {
		t.Fatal("expected Install to be called in --user mode without elevation")
	}
}

func TestAgentServiceInstall_AlreadyInstalled_SurfacesClearError(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{installErr: svcmgr.ErrAlreadyInstalled}
	withFakeSvcManager(t, m)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		agentOpts: agentServeOpts{server: "s:9091", providers: []string{"docker"}, dataDir: t.TempDir()},
	})
	if !errors.Is(err, svcmgr.ErrAlreadyInstalled) {
		t.Fatalf("error = %v, want to wrap svcmgr.ErrAlreadyInstalled", err)
	}
}

func TestAgentServiceUninstall_NotPurge_KeepsDataDir(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	dataDir := t.TempDir()
	if err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), false, dataDir); err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Fatalf("data dir should still exist: %v", err)
	}
}

func TestAgentServiceUninstall_Purge_RemovesDataDir(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	dataDir := t.TempDir()
	if err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), true, dataDir); err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if _, err := os.Stat(dataDir); err == nil {
		t.Fatal("data dir should have been removed by --purge")
	}
}

func TestAgentServiceStatus_PrintsInstalledState(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true, Running: true, Mode: "system-service"}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runAgentServiceStatus(newTestCmd(&out)); err != nil {
		t.Fatalf("runAgentServiceStatus: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "running") || !strings.Contains(got, "system-service") {
		t.Fatalf("status output = %q, expected to mention running and system-service", got)
	}
}
