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

// fakeManager stubs svcmgr.Manager for command-layer tests. It records the
// name each method was called with so tests can assert instance-name
// resolution without depending on a real OS service manager.
type fakeManager struct {
	installedSpecs []svcmgr.Spec
	installErr     error
	uninstallErr   error
	startErr       error
	stopErr        error
	status         svcmgr.Status
	statusErr      error
	// statusByName overrides status/statusErr per name when non-nil, for
	// tests that need distinct answers for different service names against
	// the same manager (e.g. #157's boxy-agent-vs-boxy-serve restart
	// check). A name absent from the map reports Status{} (not installed).
	statusByName map[string]svcmgr.Status

	uninstalledName string
	startedName     string
	stoppedName     string
	statusQueried   string

	// *Names record every call in order, for tests asserting multiple
	// Start/Stop/Status calls against one fakeManager instance. The
	// single-value fields above still hold the most recent call, for
	// existing single-call tests.
	startedNames []string
	stoppedNames []string
	statusNames  []string
}

func (f *fakeManager) Install(spec svcmgr.Spec) error {
	if f.installErr != nil {
		return f.installErr
	}
	f.installedSpecs = append(f.installedSpecs, spec)
	return nil
}
func (f *fakeManager) Uninstall(name string) error {
	f.uninstalledName = name
	return f.uninstallErr
}
func (f *fakeManager) Start(name string) error {
	f.startedName = name
	f.startedNames = append(f.startedNames, name)
	return f.startErr
}
func (f *fakeManager) Stop(name string) error {
	f.stoppedName = name
	f.stoppedNames = append(f.stoppedNames, name)
	return f.stopErr
}
func (f *fakeManager) Status(name string) (svcmgr.Status, error) {
	f.statusQueried = name
	f.statusNames = append(f.statusNames, name)
	if f.statusByName != nil {
		return f.statusByName[name], nil // zero value (not installed) if absent
	}
	return f.status, f.statusErr
}

// withPerModeFakeSvcManager routes svcmgrNewManager to a different
// fakeManager depending on ManagerOptions.UserMode, mirroring reality:
// the privileged and --user install modes are entirely different backends
// (SCM vs Task Scheduler on Windows, system vs --user systemd on Linux),
// so a service installed under one mode is genuinely invisible to the
// other's Manager.
func withPerModeFakeSvcManager(t *testing.T, system, user *fakeManager) {
	t.Helper()
	orig := svcmgrNewManager
	svcmgrNewManager = func(opts svcmgr.ManagerOptions) (svcmgr.Manager, error) {
		if opts.UserMode {
			return user, nil
		}
		return system, nil
	}
	t.Cleanup(func() { svcmgrNewManager = orig })
}

func withFakeSvcManager(t *testing.T, m *fakeManager) {
	t.Helper()
	orig := svcmgrNewManager
	svcmgrNewManager = func(svcmgr.ManagerOptions) (svcmgr.Manager, error) { return m, nil }
	t.Cleanup(func() { svcmgrNewManager = orig })
}

// withFakeSvcManagerCapturingOpts behaves like withFakeSvcManager but also
// records every ManagerOptions the command layer passed to svcmgrNewManager,
// so tests can assert --user threads through to uninstall/start/stop/status
// — those get UserMode only via ManagerOptions, unlike Install which also
// carries it implicitly through which ManagerOptions built the Manager it's
// called on.
func withFakeSvcManagerCapturingOpts(t *testing.T, m *fakeManager, captured *[]svcmgr.ManagerOptions) {
	t.Helper()
	orig := svcmgrNewManager
	svcmgrNewManager = func(opts svcmgr.ManagerOptions) (svcmgr.Manager, error) {
		*captured = append(*captured, opts)
		return m, nil
	}
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

// TestAgentServiceInstall_PersistsProviderConfigsBaseDirFromConfigFile
// guards a real bug: `agent service install --config boxy.yaml` used to
// write ProviderConfigs into service.yaml without recording the directory
// they were resolved against, so a later `agent serve --service-config
// service.yaml` recomputed the base directory as service.yaml's own
// directory (the service's data dir) instead of boxy.yaml's — silently
// resolving a relative provider path (e.g. devfactory's DataDir) in the
// wrong place. Install must now persist the resolved base directory.
func TestAgentServiceInstall_PersistsProviderConfigsBaseDirFromConfigFile(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	configDir := t.TempDir()
	boxyConfigPath := filepath.Join(configDir, "boxy.yaml")
	if err := os.WriteFile(boxyConfigPath, []byte("providers:\n  - name: docker-local\n    type: docker\n    config:\n      host: unix:///tmp/docker.sock\npools: []\n"), 0o600); err != nil {
		t.Fatalf("write boxy.yaml: %v", err)
	}

	serviceDataDir := filepath.Join(t.TempDir(), ".boxy-agent")
	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		agentOpts: agentServeOpts{
			server:     "boxy-server:9091",
			configPath: boxyConfigPath,
			dataDir:    serviceDataDir,
		},
	})
	if err != nil {
		t.Fatalf("runAgentServiceInstall: %v", err)
	}

	cfg, err := loadAgentServiceConfig(filepath.Join(serviceDataDir, "service.yaml"))
	if err != nil {
		t.Fatalf("loadAgentServiceConfig: %v", err)
	}
	wantBaseDir, err := filepath.Abs(configDir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if cfg.ProviderConfigsBaseDir != wantBaseDir {
		t.Fatalf("ProviderConfigsBaseDir = %q, want %q (boxy.yaml's directory, not service.yaml's)", cfg.ProviderConfigsBaseDir, wantBaseDir)
	}

	// End to end: resolveAgentServeOpts against the installed service.yaml
	// must recover boxy.yaml's directory, not service.yaml's own directory
	// (which is a different location entirely — serviceDataDir here).
	opts, err := resolveAgentServeOpts(agentServeOpts{serviceConfigPath: filepath.Join(serviceDataDir, "service.yaml")})
	if err != nil {
		t.Fatalf("resolveAgentServeOpts: %v", err)
	}
	if opts.providerConfigsBaseDir != wantBaseDir {
		t.Fatalf("providerConfigsBaseDir = %q, want %q", opts.providerConfigsBaseDir, wantBaseDir)
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

func TestAgentServiceInstall_NamedInstance_UsesSuffixedNameAndDataDir(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	wd := t.TempDir()
	t.Setenv("BOXY_WORKING_DIR", wd)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		instanceName: "test1",
		agentOpts:    agentServeOpts{server: "s:9091", providers: []string{"docker"}},
	})
	if err != nil {
		t.Fatalf("runAgentServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 || m.installedSpecs[0].Name != "boxy-agent-test1" {
		t.Fatalf("installedSpecs = %+v, want one Spec named boxy-agent-test1", m.installedSpecs)
	}
	wantDataDir := filepath.Join(wd, ".boxy-agent-test1")
	if _, err := os.Stat(filepath.Join(wantDataDir, "service.yaml")); err != nil {
		t.Fatalf("expected service config under %q: %v", wantDataDir, err)
	}
}

func TestAgentServiceInstall_InvalidInstanceName_ErrorsWithoutInstalling(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		instanceName: "bad name!",
		agentOpts:    agentServeOpts{server: "s:9091", providers: []string{"docker"}, dataDir: t.TempDir()},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid --instance-name")
	}
	if len(m.installedSpecs) != 0 {
		t.Fatal("must not call Install when --instance-name fails validation")
	}
}

func TestAgentServiceUninstall_NotPurge_KeepsDataDir(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	dataDir := t.TempDir()
	err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), agentServiceUninstallOpts{dataDir: dataDir})
	if err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Fatalf("data dir should still exist: %v", err)
	}
	if m.uninstalledName != "boxy-agent" {
		t.Errorf("Uninstall called with %q, want boxy-agent", m.uninstalledName)
	}
}

func TestAgentServiceUninstall_Purge_RemovesDataDir(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "service.yaml"), []byte("server: x\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), agentServiceUninstallOpts{purge: true, dataDir: dataDir})
	if err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if _, err := os.Stat(dataDir); err == nil {
		t.Fatal("data dir should have been removed by --purge")
	}
}

func TestAgentServiceUninstall_Purge_RefusesWithoutMarker(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	// A directory that exists but was never actually a boxy service data
	// directory (e.g. --instance-name typo'd, or --data-dir pointed at the
	// wrong instance's directory) must not be silently deleted.
	dataDir := t.TempDir()
	err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), agentServiceUninstallOpts{purge: true, dataDir: dataDir})
	if err == nil {
		t.Fatal("expected an error instead of purging a directory with no service.yaml")
	}
	if _, statErr := os.Stat(dataDir); statErr != nil {
		t.Fatalf("data dir should NOT have been removed: %v", statErr)
	}
}

func TestAgentServiceUninstall_NamedInstance_TargetsSuffixedName(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), agentServiceUninstallOpts{
		instanceName: "test1",
		dataDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if m.uninstalledName != "boxy-agent-test1" {
		t.Errorf("Uninstall called with %q, want boxy-agent-test1", m.uninstalledName)
	}
}

func TestAgentServiceUninstall_UserMode_ThreadsThroughToManagerOptions(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	var captured []svcmgr.ManagerOptions
	withFakeSvcManagerCapturingOpts(t, m, &captured)

	err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), agentServiceUninstallOpts{
		userMode: true,
		dataDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if len(captured) != 1 || !captured[0].UserMode {
		t.Fatalf("ManagerOptions captured = %+v, want exactly one call with UserMode=true", captured)
	}
}

func TestAgentServiceStart_UserMode_ThreadsThroughToManagerOptions(t *testing.T) {
	m := &fakeManager{}
	var captured []svcmgr.ManagerOptions
	withFakeSvcManagerCapturingOpts(t, m, &captured)

	if err := runAgentServiceStart(newTestCmd(&bytes.Buffer{}), "", true); err != nil {
		t.Fatalf("runAgentServiceStart: %v", err)
	}
	if len(captured) != 1 || !captured[0].UserMode {
		t.Fatalf("ManagerOptions captured = %+v, want exactly one call with UserMode=true", captured)
	}
	if m.startedName != "boxy-agent" {
		t.Errorf("Start called with %q, want boxy-agent", m.startedName)
	}
}

func TestAgentServiceStop_NamedInstance_TargetsSuffixedName(t *testing.T) {
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	if err := runAgentServiceStop(newTestCmd(&bytes.Buffer{}), "test1", false); err != nil {
		t.Fatalf("runAgentServiceStop: %v", err)
	}
	if m.stoppedName != "boxy-agent-test1" {
		t.Errorf("Stop called with %q, want boxy-agent-test1", m.stoppedName)
	}
}

func TestAgentServiceStatus_PrintsInstalledState(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true, Running: true, Mode: "system-service"}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runAgentServiceStatus(newTestCmd(&out), "", false); err != nil {
		t.Fatalf("runAgentServiceStatus: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "running") || !strings.Contains(got, "system-service") {
		t.Fatalf("status output = %q, expected to mention running and system-service", got)
	}
}

func TestAgentServiceStatus_NamedInstance_QueriesSuffixedNameAndPrintsIt(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true, Running: false, Mode: "user-task"}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runAgentServiceStatus(newTestCmd(&out), "test1", true); err != nil {
		t.Fatalf("runAgentServiceStatus: %v", err)
	}
	if m.statusQueried != "boxy-agent-test1" {
		t.Errorf("Status queried %q, want boxy-agent-test1", m.statusQueried)
	}
	if !strings.Contains(out.String(), "boxy-agent-test1") {
		t.Errorf("status output = %q, expected to mention the named instance", out.String())
	}
}
