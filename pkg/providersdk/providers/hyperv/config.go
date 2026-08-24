// Package hyperv provides a providersdk.Driver for Microsoft Hyper-V.
// The agent must run on the Hyper-V host with Administrator privileges;
// no remote connection config is needed.
package hyperv

import (
	"fmt"
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
}

const DefaultHostReserveMB int64 = 512

func (c *Config) effectiveHostReserveMB() (int64, error) {
	if c == nil || c.HostReserveMB == nil {
		return DefaultHostReserveMB, nil
	}
	if *c.HostReserveMB < 0 {
		return 0, fmt.Errorf("host_reserve_mb must not be negative, got %d", *c.HostReserveMB)
	}
	return *c.HostReserveMB, nil
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

// NetworkConfig describes the static IP configuration to apply inside a guest
// VM during personalization. All fields are optional within this struct, but
// StaticIP is required when the struct itself is present.
type NetworkConfig struct {
	// StaticIP is the IPv4 address to assign to the guest's primary network
	// adapter (e.g. "203.0.113.50", an RFC 5737 documentation address).
	// Required when Network is set.
	StaticIP string `json:"static_ip" yaml:"static_ip"`

	// PrefixLength is the subnet prefix length (e.g. 24 for /24). Default: 24.
	PrefixLength int `json:"prefix_length" yaml:"prefix_length"`

	// DefaultGateway is the IPv4 default gateway (e.g. "203.0.113.1"). Optional.
	DefaultGateway string `json:"default_gateway,omitempty" yaml:"default_gateway,omitempty"`

	// DNSServers is a list of DNS server IPv4 addresses to assign. Optional.
	DNSServers []string `json:"dns_servers,omitempty" yaml:"dns_servers,omitempty"`
}

// validate returns an error when the NetworkConfig is non-nil but inconsistent.
func (n *NetworkConfig) validate() error {
	if n == nil {
		return nil
	}
	if strings.TrimSpace(n.StaticIP) == "" {
		return fmt.Errorf("network.static_ip is required when network is set")
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
