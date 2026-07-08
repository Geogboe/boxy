package hyperv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	// resolveSecret resolves a persisted secret reference only when guest
	// bootstrap access is needed.
	resolveSecret func(ctx context.Context, ref providersdk.SecretRef) (string, error)

	// deleteWaitTimeout/deleteWaitInterval bound how long Delete waits for a
	// VM stuck mid-transition (e.g. "Turning Off") to reach a terminal state
	// before giving up. Zero values use production defaults; tests override
	// these to avoid real sleeps. See #118.
	deleteWaitTimeout  time.Duration
	deleteWaitInterval time.Duration
}

// ErrVMBusy indicates a VM is stuck transitioning between power states and
// did not settle within the wait window. Callers should treat this as a
// signal to back off and retry later rather than forcing removal, which can
// leave a stale vmwp.exe worker and destabilize the host's Virtual Machine
// Management service (see #118).
var ErrVMBusy = errors.New("hyperv: vm did not reach a terminal power state in time")

const (
	defaultDeleteWaitTimeout  = 30 * time.Second
	defaultDeleteWaitInterval = 3 * time.Second

	// vmStateNotFound is a sentinel returned by state-polling scripts when
	// the VM has disappeared (e.g. it finished tearing down on its own).
	vmStateNotFound = "__BOXY_NOT_FOUND__"
)

// vmTransitionalStates are Hyper-V VMState values that mean "still moving
// between power states" — not safe to force-remove against.
var vmTransitionalStates = map[string]bool{
	"starting": true,
	"stopping": true,
	"saving":   true,
	"pausing":  true,
	"resuming": true,
	"reset":    true,
}

func (d *Driver) waitTimeout() time.Duration {
	if d.deleteWaitTimeout > 0 {
		return d.deleteWaitTimeout
	}
	return defaultDeleteWaitTimeout
}

func (d *Driver) waitInterval() time.Duration {
	if d.deleteWaitInterval > 0 {
		return d.deleteWaitInterval
	}
	return defaultDeleteWaitInterval
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
	if strings.TrimSpace(cc.GuestPassword) != "" {
		return nil, fmt.Errorf("config.guest_password is no longer supported; use config.guest_password_ref")
	}
	guestUser := cc.GuestUser
	if guestUser == "" {
		if strings.EqualFold(cc.GuestOS, "linux") {
			guestUser = "admin"
		} else {
			guestUser = "Administrator"
		}
	}

	if err := d.checkHostHealth(ctx); err != nil {
		return nil, fmt.Errorf("hyperv host health check failed, refusing to provision: %w", err)
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

	// Store non-sensitive Boxy guest metadata in VM Notes for later guest access
	// and allocation-time personalization. The bootstrap secret is looked up from
	// its reference at use time instead of being persisted here.
	notes := fmt.Sprintf("boxy_guest_os=%s;boxy_guest_user=%s;boxy_guest_password_ref=%s",
		cc.GuestOS, guestUser, strings.TrimSpace(cc.GuestPasswordRef))

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
	guestPassword, err := d.resolveGuestPassword(ctx, notes)
	if err != nil {
		return nil, fmt.Errorf("resolve guest password for %s: %w", id, err)
	}

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

// checkHostHealth runs a lightweight, VM-independent Hyper-V host probe
// before attempting to provision. If VMMS is already degraded (as can
// happen after a stuck teardown, see #118), this fails fast with a clear
// error instead of letting New-VHD/New-VM run into the same degraded state
// on every reconcile pass.
func (d *Driver) checkHostHealth(ctx context.Context) error {
	_, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
Get-VMHost | Out-Null
'OK'
`)
	if err != nil {
		return fmt.Errorf("hyperv host probe (Get-VMHost) failed, VMMS may be degraded: %w", err)
	}
	return nil
}

// waitForTerminalVMState polls a VM's power state until it leaves the
// transitional set (see vmTransitionalStates) or disappears entirely. It
// never attempts to force a state change — it only observes — so a VM stuck
// in a state like "Turning Off" cannot be pushed into a worse state by this
// call. Returns ErrVMBusy if the VM is still transitioning when the wait
// timeout elapses.
func (d *Driver) waitForTerminalVMState(ctx context.Context, vmName string) (string, error) {
	deadline := time.Now().Add(d.waitTimeout())
	stateScript := fmt.Sprintf(`
$vm = Get-VM -Name '%s' -ErrorAction SilentlyContinue
if ($null -eq $vm) {
  '%s'
} else {
  $vm.State.ToString()
}
`, psq(vmName), vmStateNotFound)

	for {
		out, err := d.ps(ctx, stateScript)
		if err != nil {
			return "", fmt.Errorf("check VM state: %w", err)
		}
		state := strings.TrimSpace(out)
		if state == vmStateNotFound || !vmTransitionalStates[strings.ToLower(state)] {
			return state, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%w (name=%q, last state=%q)", ErrVMBusy, vmName, state)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(d.waitInterval()):
		}
	}
}

func (d *Driver) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("resource id is required")
	}

	infoScript := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$vm = Get-VM -Id '%s' -ErrorAction SilentlyContinue
if ($null -eq $vm) {
  '__BOXY_NOT_FOUND__'
  return
}
$vhd = (Get-VMHardDiskDrive -VMName $vm.Name | Select-Object -First 1).Path
"$($vm.Name)|$vhd|$($vm.State)"
`, psq(id))

	out, err := d.ps(ctx, infoScript)
	if err != nil {
		return fmt.Errorf("hyperv delete: get VM info for %s: %w", id, err)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "__BOXY_NOT_FOUND__" {
		return nil
	}

	parts := strings.SplitN(trimmed, "|", 3)
	vmName := ""
	vhdPath := ""
	state := ""
	if len(parts) >= 1 {
		vmName = parts[0]
	}
	if len(parts) >= 2 {
		vhdPath = parts[1]
	}
	if len(parts) >= 3 {
		state = parts[2]
	}
	if vmName == "" {
		return fmt.Errorf("hyperv delete: could not resolve VM name for id %s", id)
	}

	// Guard against forcing removal on a VM that's mid-transition (e.g.
	// stuck in "Turning Off"). Blindly forcing Stop-VM/Remove-VM against
	// such a VM is what left a stale vmwp.exe worker and destabilized VMMS
	// in #118. Wait for it to settle first; if it never does, surface
	// ErrVMBusy so the caller can back off instead of retrying immediately.
	if vmTransitionalStates[strings.ToLower(state)] {
		finalState, err := d.waitForTerminalVMState(ctx, vmName)
		if err != nil {
			return fmt.Errorf("hyperv delete VM %q: %w", vmName, err)
		}
		if finalState == vmStateNotFound {
			// VM tore itself down while we were waiting; nothing left to do.
			return nil
		}
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
	result, err := d.PersonalizeGuest(ctx, id)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.AccessDetails.ToProperties(), nil
}

func (d *Driver) PersonalizeGuest(ctx context.Context, id string) (*providersdk.GuestPersonalizationResult, error) {
	notes, err := d.readNotes(ctx, id)
	if err != nil {
		notes = map[string]string{}
	}

	guestOS := notes["boxy_guest_os"]
	if guestOS == "" {
		guestOS = "windows"
	}
	guestUser := notes["boxy_guest_user"]
	if guestUser == "" {
		if strings.EqualFold(guestOS, "linux") {
			guestUser = "admin"
		} else {
			guestUser = "Administrator"
		}
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
		return &providersdk.GuestPersonalizationResult{
			AccessDetails: providersdk.GuestAccessDetails{
				Properties: map[string]string{
					"access":   "ssh",
					"ssh_host": ip,
					"ssh_port": "22",
					"ssh_user": guestUser,
					"ssh_cmd":  fmt.Sprintf("ssh %s@%s", guestUser, ip),
				},
			},
		}, nil
	}

	// Windows: return WinRM/PSRP connection info.
	return &providersdk.GuestPersonalizationResult{
		AccessDetails: providersdk.GuestAccessDetails{
			Properties: map[string]string{
				"access":    "winrm",
				"host":      ip,
				"user":      guestUser,
				"psrp_vmid": id,
			},
		},
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
	for part := range strings.SplitSeq(notes, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return m
}

func (d *Driver) resolveGuestPassword(ctx context.Context, notes map[string]string) (string, error) {
	ref := strings.TrimSpace(notes["boxy_guest_password_ref"])
	if ref == "" {
		return "", fmt.Errorf("VM has no guest_password_ref metadata")
	}

	resolver := d.resolveSecret
	if resolver == nil {
		resolver = providersdk.ResolveSecretRef
	}

	password, err := resolver(ctx, providersdk.SecretRef(ref))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("secret ref %q resolved to an empty secret", ref)
	}
	return password, nil
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
