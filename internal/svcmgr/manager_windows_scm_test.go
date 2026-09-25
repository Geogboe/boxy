//go:build windows

package svcmgr

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// fakeSCM and fakeSCMService fake just enough of the SCM surface for
// scmManager's tests, keyed by service name.
type fakeSCM struct {
	services map[string]*fakeSCMService
	// createCalls records every (name, exepath, args) CreateService saw, in
	// order -- lets a test assert exactly what Install passed through,
	// verbatim, on its way to golang.org/x/sys/windows/svc/mgr.Mgr's own
	// CreateService, which is what actually escapes/quotes exepath and each
	// arg (via syscall.EscapeArg) before building the service's
	// BinaryPathName. Boxy's own code between Install and that call must
	// never itself split, unescape, or otherwise mangle exepath.
	createCalls []fakeCreateServiceCall
}

type fakeCreateServiceCall struct {
	name    string
	exepath string
	args    []string
}

type fakeSCMService struct {
	status  svc.Status
	deleted bool
	started bool
}

func (f *fakeSCM) OpenService(name string) (scmService, error) {
	s, ok := f.services[name]
	if !ok || s.deleted {
		return nil, errors.New("service does not exist")
	}
	return s, nil
}

func (f *fakeSCM) CreateService(name, exepath string, _ mgr.Config, args ...string) (scmService, error) {
	f.createCalls = append(f.createCalls, fakeCreateServiceCall{name: name, exepath: exepath, args: args})
	if f.services == nil {
		f.services = map[string]*fakeSCMService{}
	}
	s := &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	f.services[name] = s
	return s, nil
}

func (f *fakeSCM) Disconnect() error { return nil }

func (s *fakeSCMService) Close() error { return nil }
func (s *fakeSCMService) Delete() error {
	s.deleted = true
	return nil
}
func (s *fakeSCMService) Start(...string) error {
	s.started = true
	s.status.State = svc.Running
	return nil
}
func (s *fakeSCMService) Control(c svc.Cmd) (svc.Status, error) {
	if c == svc.Stop {
		s.status.State = svc.Stopped
	}
	return s.status, nil
}
func (s *fakeSCMService) Query() (svc.Status, error) { return s.status, nil }

func withFakeSCM(t *testing.T) *fakeSCM {
	t.Helper()
	f := &fakeSCM{services: map[string]*fakeSCMService{}}
	orig := connectSCM
	connectSCM = func() (scmAPI, error) { return f, nil }
	t.Cleanup(func() { connectSCM = orig })
	return f
}

func TestSCMManager_Install_CreatesService(t *testing.T) {
	f := withFakeSCM(t)
	m := &scmManager{}

	if err := m.Install(Spec{Name: "boxy-agent", DisplayName: "Boxy Agent", ExecPath: `C:\boxy.exe`, Args: []string{"agent", "serve"}}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, ok := f.services["boxy-agent"]; !ok {
		t.Fatal("expected boxy-agent to be created in the fake SCM")
	}
}

// TestSCMManager_Install_PassesSpacedExecPathThroughUnmodified guards
// against reintroducing a CWE-428 unquoted-service-path vulnerability if a
// future change starts building spec.ExecPath by hand (e.g. string
// concatenation) instead of passing it straight through to CreateService.
// A path under "Program Files" (spaces, #379 blocker 6's protected
// per-service install directory) is exactly the shape that would break if
// something upstream of the x/sys mgr call quoted or split it incorrectly;
// the actual escaping into the service's BinaryPathName is
// golang.org/x/sys/windows/svc/mgr.Mgr.CreateService's job (it uses
// syscall.EscapeArg), not this package's -- this test only proves Boxy's
// own code hands it the raw path, unmangled, for that library to escape.
func TestSCMManager_Install_PassesSpacedExecPathThroughUnmodified(t *testing.T) {
	f := withFakeSCM(t)
	m := &scmManager{}
	spacedExe := `C:\Program Files\Boxy\boxy-agent\boxy.exe`

	if err := m.Install(Spec{Name: "boxy-agent", ExecPath: spacedExe, Args: []string{"agent", "serve", "--service-config", `C:\Program Files\Boxy\boxy-agent\service.yaml`}}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(f.createCalls) != 1 {
		t.Fatalf("expected exactly 1 CreateService call, got %d", len(f.createCalls))
	}
	call := f.createCalls[0]
	if call.exepath != spacedExe {
		t.Fatalf("CreateService exepath = %q, want %q verbatim", call.exepath, spacedExe)
	}
	if len(call.args) != 4 || call.args[3] != `C:\Program Files\Boxy\boxy-agent\service.yaml` {
		t.Fatalf("CreateService args = %v, want the spaced service-config path preserved as one element", call.args)
	}
}

func TestSCMManager_Install_AlreadyInstalled_Errors(t *testing.T) {
	f := withFakeSCM(t)
	f.services["boxy-agent"] = &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	m := &scmManager{}

	err := m.Install(Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("Install error = %v, want ErrAlreadyInstalled", err)
	}
}

func TestSCMManager_Uninstall_DeletesService(t *testing.T) {
	f := withFakeSCM(t)
	f.services["boxy-agent"] = &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	m := &scmManager{}

	if err := m.Uninstall("boxy-agent"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !f.services["boxy-agent"].deleted {
		t.Fatal("expected service to be marked deleted")
	}
}

func TestSCMManager_Uninstall_NotInstalled_Errors(t *testing.T) {
	withFakeSCM(t)
	m := &scmManager{}
	if err := m.Uninstall("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall error = %v, want ErrNotInstalled", err)
	}
}

func TestSCMManager_StartStop_ChangeStatus(t *testing.T) {
	f := withFakeSCM(t)
	f.services["boxy-agent"] = &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	m := &scmManager{}

	if err := m.Start("boxy-agent"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Running || st.Mode != "system-service" {
		t.Fatalf("Status after Start = %+v, want Running=true Mode=system-service", st)
	}

	if err := m.Stop("boxy-agent"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st, err = m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Fatalf("Status after Stop = %+v, want Running=false", st)
	}
}
