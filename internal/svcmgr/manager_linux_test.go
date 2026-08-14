//go:build linux

package svcmgr

import (
	"errors"
	"fmt"
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

// withFakeUsername overrides currentUsername so tests get a deterministic
// value regardless of the real OS user on whatever machine runs them (unlike
// $USER, which os/user.Current() takes priority over).
func withFakeUsername(t *testing.T, name string) {
	t.Helper()
	orig := currentUsername
	currentUsername = func() string { return name }
	t.Cleanup(func() { currentUsername = orig })
}

func TestRenderUnit_ContainsExecStartAndRestart(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent",
		ExecPath:    "/usr/local/bin/boxy",
		Args:        []string{"agent", "serve", "--service-config", "/home/testuser/.boxy-agent/service.yaml"},
	}
	unit := renderUnit(spec)
	want := []string{
		"Description=Boxy remote agent",
		`ExecStart=/usr/local/bin/boxy agent serve --service-config /home/testuser/.boxy-agent/service.yaml`,
		"Restart=on-failure",
	}
	for _, w := range want {
		if !strings.Contains(unit, w) {
			t.Errorf("rendered unit missing %q; got:\n%s", w, unit)
		}
	}
}

// TestRenderUnit_QuotesArgsWithSpaces covers the reopened review finding:
// spec.Args elements are user-controlled paths (--data-dir, --config,
// --ca-cert, ...) that may contain spaces. systemd parses ExecStart= with
// its own word-splitting rules, so an unquoted spaced value would be
// silently split into extra tokens; renderUnit must quote it so it reads
// back as a single word.
func TestRenderUnit_QuotesArgsWithSpaces(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent",
		ExecPath:    "/usr/local/bin/boxy",
		Args:        []string{"agent", "serve", "--data-dir", "/opt/my boxy/data"},
	}
	unit := renderUnit(spec)
	want := `ExecStart=/usr/local/bin/boxy agent serve --data-dir "/opt/my boxy/data"`
	if !strings.Contains(unit, want) {
		t.Errorf("rendered unit missing quoted ExecStart line %q; got:\n%s", want, unit)
	}
}

// TestQuoteSystemdArg exercises quoteSystemdArg directly across the cases
// that matter for systemd's unit-file command-line syntax: simple tokens
// stay unquoted (no gratuitous quoting), and space/tab/quote/backslash
// bytes trigger quoting with backslash-escaped embedded " and \.
func TestQuoteSystemdArg(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"simple token", "agent", "agent"},
		{"absolute path no spaces", "/usr/local/bin/boxy", "/usr/local/bin/boxy"},
		{"flag token", "--data-dir", "--data-dir"},
		{"space", "/opt/my boxy/data", `"/opt/my boxy/data"`},
		{"tab", "a\tb", "\"a\tb\""},
		{"embedded double quote", `has"quote`, `"has\"quote"`},
		{"embedded backslash", `back\slash`, `"back\\slash"`},
		{"empty string", "", `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := quoteSystemdArg(tc.in)
			if got != tc.want {
				t.Errorf("quoteSystemdArg(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
	withFakeUsername(t, "testuser")
	dir := t.TempDir()
	m := &systemdManager{userMode: true, unitDir: dir}

	if err := m.Install(Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	found := false
	for _, c := range f.calls {
		if strings.Join(c, " ") == "loginctl enable-linger testuser" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a loginctl enable-linger call, got: %v", f.calls)
	}
}

// TestSystemdManager_Install_UserMode_LingerFailure_StillSucceeds covers
// review finding #2: enable-linger is best-effort. By the time it runs, the
// unit file is written and `systemctl enable --now` already succeeded, so a
// polkit-restricted or otherwise failing loginctl call must not turn a
// genuinely-installed, genuinely-running service into a reported Install
// failure.
func TestSystemdManager_Install_UserMode_LingerFailure_StillSucceeds(t *testing.T) {
	f := withFakeRunner(t)
	withFakeUsername(t, "testuser")
	f.errs["loginctl enable-linger testuser"] = fmt.Errorf("polkit: not authorized")
	dir := t.TempDir()
	m := &systemdManager{userMode: true, unitDir: dir}

	if err := m.Install(Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}); err != nil {
		t.Fatalf("Install should succeed even when loginctl enable-linger fails, got: %v", err)
	}

	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed {
		t.Errorf("expected the unit to be installed despite the linger failure, got Status=%+v", st)
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

func TestSystemdManager_Start_NotInstalled_Errors(t *testing.T) {
	withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	if err := m.Start("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Start error = %v, want ErrNotInstalled", err)
	}
}

func TestSystemdManager_Start_Installed_RunsSystemctlStart(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}
	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := m.Start(spec.Name); err != nil {
		t.Fatalf("Start: %v", err)
	}

	found := false
	for _, c := range f.calls {
		if strings.Join(c, " ") == "systemctl start boxy-agent" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a systemctl start call, got: %v", f.calls)
	}
}

func TestSystemdManager_Stop_NotInstalled_Errors(t *testing.T) {
	withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	if err := m.Stop("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Stop error = %v, want ErrNotInstalled", err)
	}
}

func TestSystemdManager_Stop_Installed_RunsSystemctlStop(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}
	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := m.Stop(spec.Name); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	found := false
	for _, c := range f.calls {
		if strings.Join(c, " ") == "systemctl stop boxy-agent" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a systemctl stop call, got: %v", f.calls)
	}
}

func TestSystemdManager_Uninstall_Installed_RemovesUnitAndRunsSystemctl(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}
	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := m.Uninstall(spec.Name); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	foundDisable := false
	for _, c := range f.calls {
		if strings.Join(c, " ") == "systemctl disable --now boxy-agent" {
			foundDisable = true
		}
	}
	if !foundDisable {
		t.Errorf("expected a systemctl disable --now call, got: %v", f.calls)
	}

	st, err := m.Status(spec.Name)
	if err != nil {
		t.Fatalf("Status after Uninstall: %v", err)
	}
	if st.Installed {
		t.Errorf("expected Installed=false after Uninstall, got Status=%+v", st)
	}
}

// TestRenderUnitFor_WantedByTarget covers review finding #4: the
// [Install] WantedBy target must be multi-user.target for a system unit and
// default.target for a --user unit — this was previously untested.
func TestRenderUnitFor_WantedByTarget(t *testing.T) {
	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}

	sysUnit := (&systemdManager{userMode: false}).renderUnitFor(spec)
	if !strings.Contains(sysUnit, "WantedBy=multi-user.target") {
		t.Errorf("system-mode unit missing WantedBy=multi-user.target; got:\n%s", sysUnit)
	}
	if strings.Contains(sysUnit, "WantedBy=default.target") {
		t.Errorf("system-mode unit unexpectedly contains WantedBy=default.target; got:\n%s", sysUnit)
	}

	userUnit := (&systemdManager{userMode: true}).renderUnitFor(spec)
	if !strings.Contains(userUnit, "WantedBy=default.target") {
		t.Errorf("user-mode unit missing WantedBy=default.target; got:\n%s", userUnit)
	}
	if strings.Contains(userUnit, "WantedBy=multi-user.target") {
		t.Errorf("user-mode unit unexpectedly contains WantedBy=multi-user.target; got:\n%s", userUnit)
	}
}
