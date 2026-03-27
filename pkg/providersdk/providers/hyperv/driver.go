package hyperv

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/psdirect"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

// Driver implements providersdk.Driver for local Hyper-V.
// VM lifecycle (New-VM, Start-VM, etc.) uses powershell.exe on the host.
// Guest exec uses PowerShell Direct via go-psrp (Windows) or SSH (Linux).
type Driver struct {
	// psExec is the host-side PowerShell execution backend (VM lifecycle ops).
	// nil → real powershell.exe; inject a mock in tests.
	psExec func(ctx context.Context, script string) (string, error)

	// guestExecFactory constructs a vmsdk.GuestExec for a given guest.
	// nil → real implementation. Inject a mock in tests.
	guestExecFactory func(vmGUID, guestOS, guestUser, guestPassword, sshHost string) vmsdk.GuestExec
}

// New creates a Hyper-V driver.
func New(_ *Config) *Driver {
	return &Driver{}
}

func (d *Driver) Type() providersdk.Type { return ProviderType }

// --- Create ---

func (d *Driver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	cc, err := decodeCreateConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("decode create config: %w", err)
	}
	if strings.TrimSpace(cc.TemplateVHD) == "" {
		return nil, fmt.Errorf("config.template_vhd is required")
	}

	// Apply defaults.
	if cc.Generation == 0 {
		cc.Generation = 2
	}
	if cc.CPUCount == 0 {
		cc.CPUCount = 2
	}
	if cc.MemoryMB == 0 {
		cc.MemoryMB = 2048
	}
	if cc.GuestOS == "" {
		cc.GuestOS = "windows"
	}
	guestUser := cc.GuestUser
	if guestUser == "" {
		if strings.EqualFold(cc.GuestOS, "linux") {
			guestUser = "admin"
		} else {
			guestUser = "Administrator"
		}
	}

	vhdDir := cc.VHDDir
	if vhdDir == "" {
		vhdDir = filepath.Dir(cc.TemplateVHD)
	}

	suffix, err := randHex(6)
	if err != nil {
		return nil, err
	}
	vmName := fmt.Sprintf("boxy-%s", suffix)
	diffPath := filepath.Join(vhdDir, vmName+".vhdx")
	memBytes := int64(cc.MemoryMB) * 1024 * 1024

	switchBlock := ""
	if strings.TrimSpace(cc.Switch) != "" {
		switchBlock = fmt.Sprintf(`
Connect-VMNetworkAdapter -VMName '%s' -SwitchName '%s' | Out-Null`,
			psq(vmName), psq(cc.Switch))
	}

	// Store boxy metadata in VM Notes for use in later Update/Allocate calls.
	notes := fmt.Sprintf("boxy_guest_os=%s;boxy_guest_user=%s;boxy_guest_password=%s",
		cc.GuestOS, guestUser, cc.GuestPassword)

	createScript := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
New-VHD -ParentPath '%s' -Path '%s' -Differencing | Out-Null
New-VM -Name '%s' -Generation %d -MemoryStartupBytes %d -VHDPath '%s' | Out-Null%s
Set-VM -Name '%s' -ProcessorCount %d | Out-Null
Set-VM -Name '%s' -Notes '%s' | Out-Null
Start-VM -Name '%s' | Out-Null
(Get-VM -Name '%s').Id.ToString()
`,
		psq(cc.TemplateVHD),
		psq(diffPath),
		psq(vmName), cc.Generation, memBytes, psq(diffPath),
		switchBlock,
		psq(vmName), cc.CPUCount,
		psq(vmName), psq(notes),
		psq(vmName),
		psq(vmName),
	)

	out, err := d.ps(ctx, createScript)
	if err != nil {
		_ = d.deleteBestEffort(ctx, vmName, diffPath)
		return nil, fmt.Errorf("hyperv create VM %q: %w", vmName, err)
	}

	vmGUID := strings.TrimSpace(out)
	if vmGUID == "" {
		_ = d.deleteBestEffort(ctx, vmName, diffPath)
		return nil, fmt.Errorf("hyperv create: empty VM GUID returned")
	}

	return &providersdk.Resource{
		ID: vmGUID,
		ConnectionInfo: map[string]string{
			"vm_name":  vmName,
			"vm_id":    vmGUID,
			"guest_os": cc.GuestOS,
		},
	}, nil
}

// --- Read ---

func (d *Driver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	out, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
(Get-VM -Id '%s').State.ToString()
`, psq(id)))
	if err != nil {
		return nil, fmt.Errorf("hyperv read %s: %w", id, err)
	}
	return &providersdk.ResourceStatus{
		ID:    id,
		State: normalizeVMState(strings.TrimSpace(out)),
	}, nil
}

func normalizeVMState(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return "running"
	case "off":
		return "stopped"
	case "saved":
		return "saved"
	case "paused":
		return "paused"
	case "starting":
		return "starting"
	case "stopping":
		return "stopping"
	case "saving":
		return "saving"
	case "pausing":
		return "pausing"
	case "resuming":
		return "resuming"
	case "reset":
		return "resetting"
	default:
		return strings.ToLower(s)
	}
}

// --- Update ---

// ExecOp runs a command on the VM guest.
type ExecOp struct {
	Command []string
}

func (d *Driver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	switch o := op.(type) {
	case *ExecOp:
		return d.execOnGuest(ctx, id, o)
	default:
		return nil, fmt.Errorf("unsupported operation type %T", op)
	}
}

func (d *Driver) execOnGuest(ctx context.Context, id string, op *ExecOp) (*providersdk.Result, error) {
	if len(op.Command) == 0 {
		return nil, fmt.Errorf("ExecOp.Command is empty")
	}

	notes, err := d.readNotes(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read VM notes for %s: %w", id, err)
	}

	guestOS := notes["boxy_guest_os"]
	if guestOS == "" {
		guestOS = "windows"
	}
	guestUser := notes["boxy_guest_user"]
	guestPassword := notes["boxy_guest_password"]

	var ge vmsdk.GuestExec
	if d.guestExecFactory != nil {
		sshHost := ""
		if strings.EqualFold(guestOS, "linux") {
			vmName, err := d.vmNameFromID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("resolve VM name for %s: %w", id, err)
			}
			sshHost, err = d.vmIP(ctx, vmName)
			if err != nil {
				return nil, fmt.Errorf("get VM IP for %s: %w", vmName, err)
			}
		}
		ge = d.guestExecFactory(id, guestOS, guestUser, guestPassword, sshHost)
	} else {
		switch strings.ToLower(guestOS) {
		case "linux":
			vmName, err := d.vmNameFromID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("resolve VM name for %s: %w", id, err)
			}
			ip, err := d.vmIP(ctx, vmName)
			if err != nil {
				return nil, fmt.Errorf("get VM IP for %s: %w", vmName, err)
			}
			ge = &vmsdk.SSHExec{Host: ip, User: guestUser, Password: guestPassword}
		default: // "windows"
			ge = psdirect.New(id, guestUser, guestPassword)
		}
	}

	cmd := op.Command[0]
	args := op.Command[1:]
	result, err := ge.Exec(ctx, cmd, args...)
	if err != nil {
		return nil, fmt.Errorf("exec on %s guest (VM %s): %w", guestOS, id, err)
	}

	return &providersdk.Result{
		Outputs: map[string]string{
			"stdout":    result.Stdout,
			"stderr":    result.Stderr,
			"exit_code": strconv.Itoa(result.ExitCode),
		},
	}, nil
}

// --- Delete ---

func (d *Driver) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("resource id is required")
	}

	infoScript := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$vm = Get-VM -Id '%s'
$vhd = (Get-VMHardDiskDrive -VMName $vm.Name | Select-Object -First 1).Path
"$($vm.Name)|$vhd"
`, psq(id))

	out, err := d.ps(ctx, infoScript)
	if err != nil {
		return fmt.Errorf("hyperv delete: get VM info for %s: %w", id, err)
	}

	parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
	vmName := ""
	vhdPath := ""
	if len(parts) >= 1 {
		vmName = parts[0]
	}
	if len(parts) >= 2 {
		vhdPath = parts[1]
	}
	if vmName == "" {
		return fmt.Errorf("hyperv delete: could not resolve VM name for id %s", id)
	}

	deleteScript := fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
Stop-VM -Name '%s' -Force -TurnOff -ErrorAction SilentlyContinue
Remove-VM -Name '%s' -Force
`, psq(vmName), psq(vmName))

	if _, err := d.ps(ctx, deleteScript); err != nil {
		return fmt.Errorf("hyperv delete VM %q: %w", vmName, err)
	}

	if vhdPath != "" {
		rmScript := fmt.Sprintf(`
if (Test-Path '%s') { Remove-Item '%s' -Force }
`, psq(vhdPath), psq(vhdPath))
		_, _ = d.ps(ctx, rmScript) // best-effort
	}

	return nil
}

// --- Allocate ---

func (d *Driver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	notes, err := d.readNotes(ctx, id)
	if err != nil {
		notes = map[string]string{}
	}

	guestOS := notes["boxy_guest_os"]
	guestUser := notes["boxy_guest_user"]
	if guestUser == "" {
		guestUser = "admin"
	}

	vmName, err := d.vmNameFromID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("resolve VM name for %s: %w", id, err)
	}

	ip, err := d.vmIP(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("get IP for VM %q: %w", vmName, err)
	}

	if strings.EqualFold(guestOS, "linux") {
		return map[string]any{
			"access":   "ssh",
			"ssh_host": ip,
			"ssh_port": "22",
			"ssh_user": guestUser,
			"ssh_cmd":  fmt.Sprintf("ssh %s@%s", guestUser, ip),
		}, nil
	}

	// Windows: return WinRM/PSRP connection info.
	return map[string]any{
		"access":    "winrm",
		"host":      ip,
		"user":      guestUser,
		"psrp_vmid": id,
	}, nil
}

// --- Helpers ---

func (d *Driver) ps(ctx context.Context, script string) (string, error) {
	if d.psExec != nil {
		return d.psExec(ctx, script)
	}
	return runPS(ctx, script)
}

func (d *Driver) vmNameFromID(ctx context.Context, id string) (string, error) {
	out, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
(Get-VM -Id '%s').Name
`, psq(id)))
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" {
		return "", fmt.Errorf("VM with id %q not found", id)
	}
	return name, nil
}

func (d *Driver) vmIP(ctx context.Context, vmName string) (string, error) {
	out, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
(Get-VMNetworkAdapter -VMName '%s').IPAddresses | Where-Object { $_ -match '^\d' } | Select-Object -First 1
`, psq(vmName)))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", fmt.Errorf("no IP address available for VM %q (is it running?)", vmName)
	}
	return ip, nil
}

func (d *Driver) readNotes(ctx context.Context, id string) (map[string]string, error) {
	out, err := d.ps(ctx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
(Get-VM -Id '%s').Notes
`, psq(id)))
	if err != nil {
		return nil, err
	}
	return parseNotes(strings.TrimSpace(out)), nil
}

func parseNotes(notes string) map[string]string {
	m := map[string]string{}
	for _, part := range strings.Split(notes, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return m
}

func (d *Driver) deleteBestEffort(ctx context.Context, vmName, vhdPath string) error {
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
Stop-VM -Name '%s' -Force -TurnOff -ErrorAction SilentlyContinue
Remove-VM -Name '%s' -Force -ErrorAction SilentlyContinue
if ('%s' -ne '' -and (Test-Path '%s')) { Remove-Item '%s' -Force -ErrorAction SilentlyContinue }
`,
		psq(vmName), psq(vmName),
		psq(vhdPath), psq(vhdPath), psq(vhdPath),
	)
	_, err := d.ps(ctx, script)
	return err
}

func decodeCreateConfig(cfg any) (CreateConfig, error) {
	switch v := cfg.(type) {
	case map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return CreateConfig{}, err
		}
		var cc CreateConfig
		if err := json.Unmarshal(b, &cc); err != nil {
			return CreateConfig{}, err
		}
		return cc, nil
	case *CreateConfig:
		return *v, nil
	case CreateConfig:
		return v, nil
	default:
		return CreateConfig{}, fmt.Errorf("unexpected config type %T", cfg)
	}
}

// psq (PowerShell quote) escapes a string for use in a PS single-quoted string.
func psq(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
