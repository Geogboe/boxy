package providersdk

import (
	"context"
	"time"
)

// Target identifies a resource instance within a provider (a VM, container, etc).
// Ref is an opaque identifier interpreted by the driver implementation.
type Target struct {
	Kind string
	Ref  string
}

type ExecResult struct {
	Stdout string
	Stderr string
}

type ExecSpec struct {
	Command []string
	Env     map[string]string
	WorkDir string
	Timeout time.Duration
}

// ExecCapability is an optional capability for running a command against a target.
type ExecCapability interface {
	Driver
	Exec(ctx context.Context, inst Instance, target Target, spec ExecSpec) (ExecResult, error)
}

// ContainerSpec is a minimal provider-agnostic container creation spec.
// Provider-specific fields can live in Options.
type ContainerSpec struct {
	Image   string
	Command []string
	Env     map[string]string
	Labels  map[string]string
	Options map[string]any
}

type ContainerHandle struct {
	ID   string
	Name string
}

// ContainerCapability is an optional capability for container lifecycle operations.
type ContainerCapability interface {
	Driver
	CreateContainer(ctx context.Context, inst Instance, spec ContainerSpec) (ContainerHandle, error)
	DeleteContainer(ctx context.Context, inst Instance, h ContainerHandle) error
}

// VMSpec is a minimal provider-agnostic VM creation spec.
// Provider-specific fields can live in Options.
type VMSpec struct {
	TemplateRef string
	CPU         int
	MemoryMB    int
	DiskGB      int
	Network     string
	Tags        map[string]string
	Options     map[string]any
}

type VMHandle struct {
	ID   string
	Name string
}

// VMCapability is an optional capability for VM lifecycle operations.
type VMCapability interface {
	Driver
	CreateVM(ctx context.Context, inst Instance, spec VMSpec) (VMHandle, error)
	DeleteVM(ctx context.Context, inst Instance, h VMHandle) error
}

// GuestCustomizations are the "comparable" customization buckets you called out.
// Fields are intentionally high-level and declarative.
type GuestCustomizations struct {
	Packages []string

	Groups []string
	Users  []GuestUser

	FirewallRules []FirewallRule
	Services      []Service
}

type GuestUser struct {
	Name   string
	Groups []string
}

type FirewallRule struct {
	ID string
}

type ServiceState string

const (
	ServiceEnabled  ServiceState = "enabled"
	ServiceDisabled ServiceState = "disabled"
)

type Service struct {
	Name  string
	State ServiceState
}

// GuestOpsCapability is an optional capability for inspecting and applying guest
// customizations (packages/users/firewall/services). The transport used to reach
// the guest (docker exec, WinRM, PowerShell Direct, guest agent, etc) is left to
// the driver implementation.
type GuestOpsCapability interface {
	Driver

	InspectGuest(ctx context.Context, inst Instance, target Target) (GuestCustomizations, error)
	ApplyGuest(ctx context.Context, inst Instance, target Target, desired GuestCustomizations) error
}
