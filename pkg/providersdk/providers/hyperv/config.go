// Package hyperv provides a providersdk.Driver for Microsoft Hyper-V.
// The agent must run on the Hyper-V host with Administrator privileges;
// no remote connection config is needed.
package hyperv

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
)

// ProviderType is the registry key for Hyper-V providers.
const ProviderType = "hyperv"

// Config holds provider-level settings. These settings apply to the entire
// Hyper-V host, not to an individual pool or VM.
type Config struct {
	// HostReserveMB is the memory headroom kept available for the host OS and
	// other processes. nil uses the safe 512 MB default; a pointer preserves
	// the explicit zero value, which disables the reserve.
	HostReserveMB *int64 `json:"host_reserve_mb,omitempty" yaml:"host_reserve_mb,omitempty"`

	// DataDir is the directory where this Hyper-V provider's restart-safe
	// state is persisted — currently just the range-based IP allocation
	// ledger (see NetworkConfig.Range, ADR-0012). Relative paths resolve
	// against the boxy config file's own directory when one is known (see
	// ResolveRelativePaths); against the process's working directory
	// otherwise. Empty defaults to ".boxy-agent/hyperv" via the same
	// resolution.
	DataDir string `json:"data_dir,omitempty" yaml:"data_dir,omitempty"`
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

// ResolveRelativePaths implements providersdk.RelativePathResolver.
//
// Unlike devfactory's ResolveRelativePaths — which leaves an empty DataDir
// alone, since devfactory falls back to a throwaway temp directory instead
// — an empty DataDir here is defaulted to ".boxy-agent/hyperv" *before*
// anchoring against baseDir. A lost ledger directly reproduces the address
// collision #222 exists to fix, so an installed agent service (which
// persists ProviderConfigsBaseDir) and an interactive `boxy agent serve`
// sharing the same --config must resolve to the same ledger file even
// though their process working directories differ. See ADR-0012.
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
	Switch string `json:"switch" yaml:"switch"`

	// Network holds optional static IP configuration to apply inside the guest
	// during personalization. When omitted the guest relies on DHCP or a
	// pre-configured address. Use this on Windows Server hosts where Hyper-V
	// does not issue DHCP leases automatically.
	Network *NetworkConfig `json:"network,omitempty" yaml:"network,omitempty"`

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

// NetworkConfig describes how to assign an IPv4 address to a guest VM
// during personalization. Exactly one of StaticIP or Range must be set.
type NetworkConfig struct {
	// StaticIP is a single fixed IPv4 address (e.g. "203.0.113.50", an RFC
	// 5737 documentation address) assigned to every VM this pool creates.
	// Mutually exclusive with Range. Only safe for a pool that never has
	// more than one VM alive at once — a pool with min_ready > 1, or that
	// preheats multiple VMs before allocation, collides on the wire. Use
	// Range for those. See ADR-0012.
	StaticIP string `json:"static_ip,omitempty" yaml:"static_ip,omitempty"`

	// Range is an IPv4 CIDR (e.g. "203.0.113.0/24") that a per-agent,
	// restart-safe ledger allocates distinct addresses from at allocation
	// time — one per resource, released back to the range on delete.
	// Mutually exclusive with StaticIP. See ADR-0012.
	Range string `json:"range,omitempty" yaml:"range,omitempty"`

	// PrefixLength is the subnet prefix length (e.g. 24 for /24) applied
	// alongside StaticIP. Default: 24. Not used in Range mode — the prefix
	// there comes from Range's own CIDR bits, which is authoritative.
	PrefixLength int `json:"prefix_length" yaml:"prefix_length"`

	// DefaultGateway is the IPv4 default gateway (e.g. "203.0.113.1").
	// Optional in both modes; in Range mode it is also excluded from
	// allocation so no VM is ever assigned the gateway's own address.
	DefaultGateway string `json:"default_gateway,omitempty" yaml:"default_gateway,omitempty"`

	// DNSServers is a list of DNS server IPv4 addresses to assign. Optional.
	DNSServers []string `json:"dns_servers,omitempty" yaml:"dns_servers,omitempty"`
}

// validate returns an error when the NetworkConfig is non-nil but inconsistent.
func (n *NetworkConfig) validate() error {
	if n == nil {
		return nil
	}
	hasStatic := strings.TrimSpace(n.StaticIP) != ""
	hasRange := strings.TrimSpace(n.Range) != ""
	switch {
	case hasStatic && hasRange:
		return fmt.Errorf("network.static_ip and network.range are mutually exclusive; set only one")
	case !hasStatic && !hasRange:
		return fmt.Errorf("network.static_ip or network.range is required when network is set")
	}
	if hasRange {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(n.Range))
		if err != nil {
			return fmt.Errorf("network.range %q is not a valid CIDR: %w", n.Range, err)
		}
		if !prefix.Addr().Is4() {
			return fmt.Errorf("network.range %q must be an IPv4 CIDR", n.Range)
		}
	}
	if hasStatic {
		addr, err := netip.ParseAddr(strings.TrimSpace(n.StaticIP))
		if err != nil {
			return fmt.Errorf("network.static_ip %q is not a valid IP address: %w", n.StaticIP, err)
		}
		if !addr.Is4() {
			return fmt.Errorf("network.static_ip %q must be an IPv4 address", n.StaticIP)
		}
	}
	if n.PrefixLength < 0 || n.PrefixLength > 32 {
		return fmt.Errorf("network.prefix_length must be between 0 and 32, got %d", n.PrefixLength)
	}
	return nil
}

// effectivePrefixLength returns PrefixLength, defaulting to 24.
func (n *NetworkConfig) effectivePrefixLength() int {
	if n.PrefixLength == 0 {
		return 24
	}
	return n.PrefixLength
}
