// internal/cli/serve_service_test.go
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/svcmgr"
)

func TestServeServiceInstall_Elevated_WritesConfigAndInstalls(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	dir := t.TempDir()
	boxyDir := filepath.Join(dir, ".boxy")

	err := runServeServiceInstall(newTestCmd(&bytes.Buffer{}), serveServiceInstallOpts{
		userMode: false,
		serveOpts: serveOpts{
			configPath: filepath.Join(dir, "boxy.yaml"),
			listen:     ":9090",
			ui:         true,
		},
		boxyDir: boxyDir,
	})
	if err != nil {
		t.Fatalf("runServeServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 || m.installedSpecs[0].Name != "boxy-serve" {
		t.Fatalf("installedSpecs = %+v, want one Spec named boxy-serve", m.installedSpecs)
	}

	cfgPath := filepath.Join(boxyDir, "service.yaml")
	cfg, err := loadServeServiceConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadServeServiceConfig: %v", err)
	}
	if cfg.Listen != ":9090" || !cfg.UI {
		t.Fatalf("saved config = %+v, unexpected", cfg)
	}

	wantArgs := []string{"serve", "--service-config", cfgPath, "--log-file", cfg.LogFile}
	if !slices.Equal(m.installedSpecs[0].Args, wantArgs) {
		t.Fatalf("Spec.Args = %v, want %v (log file must be a literal arg — root's --log-file persistent flag is only read off the cmdline, not from the service config)", m.installedSpecs[0].Args, wantArgs)
	}
}

func TestServeServiceInstall_NotElevated_ErrorsWithoutInstalling(t *testing.T) {
	withElevated(t, false)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runServeServiceInstall(newTestCmd(&bytes.Buffer{}), serveServiceInstallOpts{boxyDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when not elevated and --user was not passed")
	}
	if len(m.installedSpecs) != 0 {
		t.Fatal("must not call Install when the elevation check fails")
	}
}

func TestServeServiceInstall_NamedInstance_UsesSuffixedNameAndDir(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	dir := t.TempDir()

	err := runServeServiceInstall(newTestCmd(&bytes.Buffer{}), serveServiceInstallOpts{
		instanceName: "test1",
		serveOpts:    serveOpts{configPath: filepath.Join(dir, "boxy.yaml")},
	})
	if err != nil {
		t.Fatalf("runServeServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 || m.installedSpecs[0].Name != "boxy-serve-test1" {
		t.Fatalf("installedSpecs = %+v, want one Spec named boxy-serve-test1", m.installedSpecs)
	}
	wantBoxyDir := filepath.Join(dir, ".boxy-test1")
	if _, err := os.Stat(filepath.Join(wantBoxyDir, "service.yaml")); err != nil {
		t.Fatalf("expected service config under %q: %v", wantBoxyDir, err)
	}
}

func TestServeServiceInstall_InvalidInstanceName_ErrorsWithoutInstalling(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runServeServiceInstall(newTestCmd(&bytes.Buffer{}), serveServiceInstallOpts{
		instanceName: "bad name!",
		boxyDir:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected an error for an invalid --instance-name")
	}
	if len(m.installedSpecs) != 0 {
		t.Fatal("must not call Install when --instance-name fails validation")
	}
}

func TestServeServiceUninstall_Purge_RefusesWithoutMarker(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	boxyDir := t.TempDir()
	err := runServeServiceUninstall(newTestCmd(&bytes.Buffer{}), serveServiceUninstallOpts{purge: true, boxyDir: boxyDir})
	if err == nil {
		t.Fatal("expected an error instead of purging a directory with no service.yaml")
	}
	if _, statErr := os.Stat(boxyDir); statErr != nil {
		t.Fatalf("boxy dir should NOT have been removed: %v", statErr)
	}
}

func TestServeServiceUninstall_Purge_RemovesMarkedDir(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	boxyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(boxyDir, "service.yaml"), []byte("listen: :9090\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := runServeServiceUninstall(newTestCmd(&bytes.Buffer{}), serveServiceUninstallOpts{purge: true, boxyDir: boxyDir}); err != nil {
		t.Fatalf("runServeServiceUninstall: %v", err)
	}
	if _, err := os.Stat(boxyDir); err == nil {
		t.Fatal("boxy dir should have been removed by --purge")
	}
}

func TestServeServiceUninstall_NamedInstance_TargetsSuffixedName(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	err := runServeServiceUninstall(newTestCmd(&bytes.Buffer{}), serveServiceUninstallOpts{
		instanceName: "test1",
		boxyDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runServeServiceUninstall: %v", err)
	}
	if m.uninstalledName != "boxy-serve-test1" {
		t.Errorf("Uninstall called with %q, want boxy-serve-test1", m.uninstalledName)
	}
}

func TestServeServiceUninstall_UserMode_ThreadsThroughToManagerOptions(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	var captured []svcmgr.ManagerOptions
	withFakeSvcManagerCapturingOpts(t, m, &captured)

	err := runServeServiceUninstall(newTestCmd(&bytes.Buffer{}), serveServiceUninstallOpts{
		userMode: true,
		boxyDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runServeServiceUninstall: %v", err)
	}
	if len(captured) != 1 || !captured[0].UserMode {
		t.Fatalf("ManagerOptions captured = %+v, want exactly one call with UserMode=true", captured)
	}
}

func TestServeServiceStart_UserMode_ThreadsThroughToManagerOptions(t *testing.T) {
	m := &fakeManager{}
	var captured []svcmgr.ManagerOptions
	withFakeSvcManagerCapturingOpts(t, m, &captured)

	if err := runServeServiceStart(newTestCmd(&bytes.Buffer{}), "", true); err != nil {
		t.Fatalf("runServeServiceStart: %v", err)
	}
	if len(captured) != 1 || !captured[0].UserMode {
		t.Fatalf("ManagerOptions captured = %+v, want exactly one call with UserMode=true", captured)
	}
	if m.startedName != "boxy-serve" {
		t.Errorf("Start called with %q, want boxy-serve", m.startedName)
	}
}

func TestServeServiceStop_NamedInstance_TargetsSuffixedName(t *testing.T) {
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	if err := runServeServiceStop(newTestCmd(&bytes.Buffer{}), "test1", false); err != nil {
		t.Fatalf("runServeServiceStop: %v", err)
	}
	if m.stoppedName != "boxy-serve-test1" {
		t.Errorf("Stop called with %q, want boxy-serve-test1", m.stoppedName)
	}
}

func TestServeServiceStatus_ReportsNotInstalled(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: false}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runServeServiceStatus(newTestCmd(&out), "", false); err != nil {
		t.Fatalf("runServeServiceStatus: %v", err)
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Fatalf("status output = %q, expected to mention not installed", out.String())
	}
}

func TestServeServiceStatus_NamedInstance_QueriesSuffixedNameAndPrintsIt(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true, Running: true, Mode: "system-unit"}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runServeServiceStatus(newTestCmd(&out), "test1", false); err != nil {
		t.Fatalf("runServeServiceStatus: %v", err)
	}
	if m.statusQueried != "boxy-serve-test1" {
		t.Errorf("Status queried %q, want boxy-serve-test1", m.statusQueried)
	}
	if !strings.Contains(out.String(), "boxy-serve-test1") {
		t.Errorf("status output = %q, expected to mention the named instance", out.String())
	}
}
