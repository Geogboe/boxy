// Package hyperv provides a providersdk.Driver for Microsoft Hyper-V.
// The agent must run on the Hyper-V host with Administrator privileges;
// no remote connection config is needed.
package hyperv

import (
	"fmt"
	"path/filepath"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// ProviderType is the registry key for Hyper-V providers.
const ProviderType = "hyperv"

// Config holds provider-level settings. These settings apply to the entire
// Hyper-V host, not to an individual pool or VM.
type Config struct {
	// MemoryBudgetMB is the maximum aggregate startup memory Boxy may assign
	// to boxy-* VMs on this provider host. It is required and shared by every
	// pool using the provider.
	MemoryBudgetMB *int64 `json:"memory_budget_mb" yaml:"memory_budget_mb"`

	// HostReserveMB is the memory headroom kept available for the host OS and
	// other processes. nil uses the safe 512 MB default; a pointer preserves
	// the explicit zero value, which disables the reserve.
	HostReserveMB *int64 `json:"host_reserve_mb,omitempty" yaml:"host_reserve_mb,omitempty"`

	// DataDir is the directory where this Hyper-V provider's restart-safe
	// state is persisted — currently the per-sandbox network segment ledger
	// (see network_isolation.go, ADR-0021). Relative paths resolve
	// against the boxy config file's own directory when one is known (see
	// ResolveRelativePaths); against the process's working directory
	// otherwise. Empty defaults to ".boxy-agent/hyperv" via the same
	// resolution.
	DataDir string `json:"data_dir,omitempty" yaml:"data_dir,omitempty"`

	// MeshEndpoint is the host:port this agent should be dialed at by a
	// peer agent's WireGuard interface for cross-host sandbox traffic.
	// Explicit, operator-declared -- not auto-detected -- matching this
	// package's existing posture for anything host-identity-shaped (see
	// DataDir's doc comment): a multi-homed host has no reliable way for
	// this code to guess which of its addresses another host can actually
	// reach. Required only when MeshPeerer is actually used (a sandbox
	// spanning this host and another); a single-host-only deployment never
	// needs it set.
	MeshEndpoint string `json:"mesh_endpoint,omitempty" yaml:"mesh_endpoint,omitempty"`
}

const DefaultHostReserveMB int64 = 512

// defaultDataDirBase/defaultDataDirHyperV form Config.DataDir's default
// (".boxy-agent/hyperv") when left unset. ".boxy-agent" mirrors the
// existing convention internal/cli/agent_serve.go's own --data-dir default
// already uses for the agent's issued credentials.
const (
	defaultDataDirBase   = ".boxy-agent"
	defaultDataDirHyperV = "hyperv"
)

func (c *Config) effectiveHostReserveMB() (int64, error) {
	if c == nil || c.HostReserveMB == nil {
		return DefaultHostReserveMB, nil
	}
	if *c.HostReserveMB < 0 {
		return 0, fmt.Errorf("host_reserve_mb must not be negative, got %d", *c.HostReserveMB)
	}
	return *c.HostReserveMB, nil
}

func (c *Config) effectiveMemoryBudgetMB() (int64, error) {
	if c == nil || c.MemoryBudgetMB == nil {
		return 0, fmt.Errorf("memory_budget_mb is required")
	}
	if *c.MemoryBudgetMB <= 0 {
		return 0, fmt.Errorf("memory_budget_mb must be positive, got %d", *c.MemoryBudgetMB)
	}
	return *c.MemoryBudgetMB, nil
}

// ResolveRelativePaths implements providersdk.RelativePathResolver.
//
// Unlike devfactory's ResolveRelativePaths — which leaves an empty DataDir
// alone, since devfactory falls back to a throwaway temp directory instead
// — an empty DataDir here is defaulted to ".boxy-agent/hyperv" *before*
// anchoring against baseDir. A lost ledger means losing track of which
// per-sandbox /29 blocks are in use, so an installed agent service (which
// persists ProviderConfigsBaseDir) and an interactive `boxy agent serve`
// sharing the same --config must resolve to the same ledger file even
// though their process working directories differ. Originally introduced
// for #222's IP-range ledger (ADR-0012, now superseded); the segment ledger
// it now anchors has the same requirement, see ADR-0021.
func (c *Config) ResolveRelativePaths(baseDir string) {
	if c.DataDir == "" {
		c.DataDir = filepath.Join(defaultDataDirBase, defaultDataDirHyperV)
	}
	if baseDir == "" || filepath.IsAbs(c.DataDir) {
		return
	}
	c.DataDir = filepath.Join(baseDir, c.DataDir)
}

// CreateConfig holds pool-level settings for creating a Hyper-V VM.
type CreateConfig struct {
	// TemplateVHD is the path to the parent VHD/VHDX used for differencing disks.
	// Required.
	TemplateVHD string `json:"template_vhd" yaml:"template_vhd"`

	// Source is a provider-neutral local path or short-lived signed URL for a
	// VHD/VHDX. It is consumed during Create and is never written to VM notes
	// or returned as resource metadata.
	Source *providersdk.SourceDescriptor `json:"source,omitempty" yaml:"source,omitempty"`

	// VHDDir is the directory where differencing VHDs are created.
	// Defaults to the directory containing TemplateVHD.
	VHDDir string `json:"vhd_dir" yaml:"vhd_dir"`

	// Generation is the Hyper-V VM generation (1 or 2). Default: 2.
	Generation int `json:"generation" yaml:"generation"`

	// CPUCount is the number of virtual processors. Default: 2.
	CPUCount int `json:"cpu_count" yaml:"cpu_count"`

	// MemoryMB is startup memory in megabytes. Default: 2048.
	MemoryMB int `json:"memory_mb" yaml:"memory_mb"`

	// Switch is the name of the virtual switch to connect to. Optional.
	//
	// This is the switch a pooled VM lives on before any sandbox claims it.
	// It is not the guest's final network: per-sandbox network isolation
	// (providersdk.NetworkIsolator) moves each claimed VM onto its sandbox's
	// own Internal switch at allocation time and addresses the guest from
	// that segment's block. See ADR-0021.
	Switch string `json:"switch" yaml:"switch"`

	// GuestOS is the guest operating system: "windows" or "linux". Default: "windows".
	// Windows guests use PowerShell Direct (psdirect); Linux guests use SSH.
	GuestOS string `json:"guest_os" yaml:"guest_os"`

	// GuestUser is the guest OS username for exec operations.
	// Windows guests: used for PSRP authentication. Default: "Administrator".
	// Linux guests: used as the SSH username. Default: "admin".
	GuestUser string `json:"guest_user" yaml:"guest_user"`

	// GuestPasswordRef is an opaque lookup handle for the guest OS password.
	// Windows guests: PSRP password. Linux guests: SSH password.
	//
	// Supported built-in forms:
	//   - env:NAME
	GuestPasswordRef string `json:"guest_password_ref" yaml:"guest_password_ref"`

	// GuestPassword is deprecated and no longer used for bootstrap guest access.
	// Use GuestPasswordRef instead so the raw secret does not have to be persisted.
	GuestPassword string `json:"guest_password" yaml:"guest_password"`
}

// NOTE: a pool-level `network` block (static_ip/range/prefix_length/
// default_gateway/dns_servers) existed here until 2026-09-14 and was removed
// with #224's Plan 1c. Per-sandbox network isolation is automatic and has no
// opt-out for this driver, so the segment — not the pool — always supplies a
// claimed guest's real address, and a pool-declared one could only ever be
// stale. See ADR-0012's and ADR-0013's 2026-09-14 change-log entries, and
// AttachToSegment for what replaced it. CreateConfig decoding is lenient, so
// a `network:` block left in an existing pool config is ignored rather than
// rejected.
