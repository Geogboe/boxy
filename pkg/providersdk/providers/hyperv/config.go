// Package hyperv provides a providersdk.Driver for Microsoft Hyper-V.
// The agent must run on the Hyper-V host with Administrator privileges;
// no remote connection config is needed.
package hyperv

// ProviderType is the registry key for Hyper-V providers.
const ProviderType = "hyperv"

// Config holds provider-level settings. Currently empty because the agent
// runs directly on the Hyper-V host and uses the local PowerShell module.
type Config struct{}

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

	// GuestOS is the guest operating system: "windows" or "linux". Default: "windows".
	// Windows guests use PowerShell Direct (psdirect); Linux guests use SSH.
	GuestOS string `json:"guest_os" yaml:"guest_os"`

	// GuestUser is the guest OS username for exec operations.
	// Windows guests: used for PSRP authentication. Default: "Administrator".
	// Linux guests: used as the SSH username. Default: "admin".
	GuestUser string `json:"guest_user" yaml:"guest_user"`

	// GuestPassword is the guest OS password.
	// Windows guests: PSRP password. Linux guests: SSH password.
	GuestPassword string `json:"guest_password" yaml:"guest_password"`
}
