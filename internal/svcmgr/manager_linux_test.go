//go:build linux

package svcmgr

import (
	"errors"
	"strings"
	"testing"
)

// fakeRunner records every invocation and returns canned output/errors
// keyed by the joined command line, so tests can assert exactly which
// systemctl/loginctl calls Install/Uninstall/Start/Stop/Status make.
type fakeRunner struct {
	calls   [][]string
	outputs map[string][]byte
	errs    map[string]error
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	return f.outputs[key], f.errs[key]
}

func withFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	f := &fakeRunner{outputs: map[string][]byte{}, errs: map[string]error{}}
	orig := runCommand
	runCommand = f.run
	t.Cleanup(func() { runCommand = orig })
	return f
}

func TestRenderUnit_ContainsExecStartAndRestart(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent",
		ExecPath:    "/usr/local/bin/boxy",
		Args:        []string{"agent", "serve", "--service-config", "/home/u/.boxy-agent/service.yaml"},
	}
	unit := renderUnit(spec)
	want := []string{
		"Description=Boxy remote agent",
		`ExecStart=/usr/local/bin/boxy agent serve --service-config /home/u/.boxy-agent/service.yaml`,
		"Restart=on-failure",
	}
	for _, w := range want {
		if !strings.Contains(unit, w) {
			t.Errorf("rendered unit missing %q; got:\n%s", w, unit)
		}
	}
}

func TestSystemdManager_Install_SystemMode_RunsExpectedCommands(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}

	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy", Args: []string{"agent", "serve"}}
	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}

	wantCalls := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", "boxy-agent"},
	}
	if len(f.calls) != len(wantCalls) {
		t.Fatalf("got %d systemctl calls, want %d: %v", len(f.calls), len(wantCalls), f.calls)
	}
	for i, want := range wantCalls {
		got := strings.Join(f.calls[i], " ")
		if got != strings.Join(want, " ") {
			t.Errorf("call %d = %q, want %q", i, got, strings.Join(want, " "))
		}
	}
}

func TestSystemdManager_Install_UserMode_EnablesLinger(t *testing.T) {
	f := withFakeRunner(t)
	t.Setenv("USER", "geo")
	dir := t.TempDir()
	m := &systemdManager{userMode: true, unitDir: dir}

	if err := m.Install(Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	found := false
	for _, c := range f.calls {
		if strings.Join(c, " ") == "loginctl enable-linger geo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a loginctl enable-linger call, got: %v", f.calls)
	}
}

func TestSystemdManager_Install_AlreadyInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}

	if err := m.Install(spec); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	err := m.Install(spec)
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("second Install error = %v, want ErrAlreadyInstalled", err)
	}
	_ = f
}

func TestSystemdManager_Uninstall_NotInstalled_Errors(t *testing.T) {
	withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	if err := m.Uninstall("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall error = %v, want ErrNotInstalled", err)
	}
}

func TestSystemdManager_Status_ReportsMode(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: true, unitDir: dir}
	if err := m.Install(Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	f.outputs["systemctl --user is-active boxy-agent"] = []byte("active\n")

	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running || st.Mode != "user-unit" {
		t.Fatalf("Status = %+v, want Installed=true Running=true Mode=user-unit", st)
	}
}
