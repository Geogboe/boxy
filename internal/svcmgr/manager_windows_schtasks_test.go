//go:build windows

package svcmgr

import (
	"errors"
	"strings"
	"testing"
)

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

func TestRenderTaskXML_ContainsLogonTriggerHiddenAndRestart(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent",
		ExecPath:    `C:\Users\geo\.local\bin\boxy.exe`,
		Args:        []string{"agent", "serve", "--service-config", `C:\Users\geo\.boxy-agent\service.yaml`},
	}
	xml := renderTaskXML(spec)
	for _, want := range []string{
		"<LogonTrigger>",
		"<Hidden>true</Hidden>",
		"<RestartOnFailure>",
		`<Command>C:\Users\geo\.local\bin\boxy.exe</Command>`,
		`<Arguments>agent serve --service-config C:\Users\geo\.boxy-agent\service.yaml</Arguments>`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("rendered task XML missing %q; got:\n%s", want, xml)
		}
	}
}

func TestTaskSchedulerManager_Install_RunsSchtasksCreate(t *testing.T) {
	f := withFakeRunner(t)
	m := &taskSchedulerManager{}
	spec := Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`, Args: []string{"agent", "serve"}}

	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}
	var create []string
	for _, c := range f.calls {
		if len(c) > 1 && c[0] == "schtasks" && c[1] == "/create" {
			create = c
		}
	}
	if create == nil {
		t.Fatalf("expected a schtasks /create call, got: %v", f.calls)
	}
	joined := strings.Join(create, " ")
	for _, want := range []string{"/create", "/tn", "boxy-agent", "/xml", "/f"} {
		if !strings.Contains(joined, want) {
			t.Errorf("schtasks call missing %q: %s", want, joined)
		}
	}
}

func TestTaskSchedulerManager_Install_AlreadyInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	m := &taskSchedulerManager{}
	f.outputs["schtasks /query /tn boxy-agent"] = []byte("boxy-agent  Ready")

	err := m.Install(Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("Install error = %v, want ErrAlreadyInstalled", err)
	}
}

func TestTaskSchedulerManager_Uninstall_NotInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	f.errs["schtasks /query /tn boxy-agent"] = errors.New("ERROR: The system cannot find the file specified.")
	m := &taskSchedulerManager{}

	if err := m.Uninstall("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall error = %v, want ErrNotInstalled", err)
	}
}

func TestTaskSchedulerManager_Status_ReportsUserTaskMode(t *testing.T) {
	f := withFakeRunner(t)
	f.outputs["schtasks /query /tn boxy-agent"] = []byte("TaskName  Status\r\nboxy-agent  Running\r\n")
	m := &taskSchedulerManager{}

	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running || st.Mode != "user-task" {
		t.Fatalf("Status = %+v, want Installed=true Running=true Mode=user-task", st)
	}
}
