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

func (f *fakeSCM) CreateService(name, _ string, _ mgr.Config, _ ...string) (scmService, error) {
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
