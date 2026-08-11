//go:build windows

package svcmgr

import (
	"fmt"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// scmAPI and scmService narrow the mgr.Mgr/mgr.Service surface this
// package needs, so tests can fake the Windows Service Control Manager
// without requiring Administrator rights or a real Windows host. *mgr.Mgr
// and *mgr.Service satisfy these structurally (see realSCM below) — no
// wrapper methods needed beyond the OpenService/CreateService return-type
// adaptation.
type scmAPI interface {
	OpenService(name string) (scmService, error)
	CreateService(name, exepath string, c mgr.Config, args ...string) (scmService, error)
	Disconnect() error
}

type scmService interface {
	Close() error
	Delete() error
	Start(args ...string) error
	Control(c svc.Cmd) (svc.Status, error)
	Query() (svc.Status, error)
}

type realSCM struct{ m *mgr.Mgr }

func (r realSCM) OpenService(name string) (scmService, error) { return r.m.OpenService(name) }

func (r realSCM) CreateService(name, exepath string, c mgr.Config, args ...string) (scmService, error) {
	return r.m.CreateService(name, exepath, c, args...)
}

func (r realSCM) Disconnect() error { return r.m.Disconnect() }

// connectSCM is overridable in tests so Install/Uninstall/Start/Stop/Status
// can be unit-tested without a real, elevated SCM connection.
var connectSCM = func() (scmAPI, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	return realSCM{m}, nil
}

// scmManager installs boxy as a real Windows Service via the Service
// Control Manager. Requires an elevated (Administrator) process — callers
// must check privilege before constructing/using this (see
// internal/cli's isElevated).
type scmManager struct{}

func (m *scmManager) Install(spec Spec) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	if existing, err := scm.OpenService(spec.Name); err == nil {
		_ = existing.Close()
		return ErrAlreadyInstalled
	}

	svcCfg := mgr.Config{
		DisplayName: spec.DisplayName,
		Description: spec.Description,
		StartType:   mgr.StartAutomatic,
	}
	s, err := scm.CreateService(spec.Name, spec.ExecPath, svcCfg, spec.Args...)
	if err != nil {
		return fmt.Errorf("create service %q: %w", spec.Name, err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q after install: %w", spec.Name, err)
	}
	return nil
}

func (m *scmManager) Uninstall(name string) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return ErrNotInstalled
	}
	defer func() { _ = s.Close() }()

	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", name, err)
	}
	return nil
}

func (m *scmManager) Start(name string) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return ErrNotInstalled
	}
	defer func() { _ = s.Close() }()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q: %w", name, err)
	}
	return nil
}

func (m *scmManager) Stop(name string) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return ErrNotInstalled
	}
	defer func() { _ = s.Close() }()

	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service %q: %w", name, err)
	}
	return nil
}

func (m *scmManager) Status(name string) (Status, error) {
	scm, err := connectSCM()
	if err != nil {
		return Status{}, fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return Status{}, nil
	}
	defer func() { _ = s.Close() }()

	st, err := s.Query()
	if err != nil {
		return Status{}, fmt.Errorf("query service %q: %w", name, err)
	}
	return Status{Installed: true, Running: st.State == svc.Running, Mode: "system-service"}, nil
}
