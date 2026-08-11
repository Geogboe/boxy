// internal/cli/serve_service_test.go
package cli

import (
	"bytes"
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

func TestServeServiceStatus_ReportsNotInstalled(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: false}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runServeServiceStatus(newTestCmd(&out)); err != nil {
		t.Fatalf("runServeServiceStatus: %v", err)
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Fatalf("status output = %q, expected to mention not installed", out.String())
	}
}
