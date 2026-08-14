package hyperv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
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

	// memoryQueryTimeout bounds the live available-memory PowerShell query
	// that reserveMemory/Availability run while holding mu. It applies on top of
	// (never beyond) the caller's ctx, so a hung query can't wedge mu — and
	// with it every other Create on this driver — indefinitely on a daemon
	// ctx that has no deadline of its own. Zero uses the production default;
	// tests override to avoid real sleeps.
	memoryQueryTimeout time.Duration

	// deleteBestEffortInterval bounds the pause between deleteBestEffort's
	// cleanup-retry attempts. Zero uses the production default; tests
	// override it to avoid real sleeps. See #174.
	deleteBestEffortInterval time.Duration

	// reservationGraceInterval delays reserveMemory's released decrement.
	// Zero uses the production default; tests override it to avoid real
	// sleeps. See #183.
	reservationGraceInterval time.Duration

	// mu guards reservedMB, the memory (in MB) committed to in-flight Create
	// calls that a live host query doesn't reflect yet. See reserveMemory.
	mu         sync.Mutex
	reservedMB int64
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

	// defaultMemoryQueryTimeout bounds reserveMemory/Availability's live
	// PowerShell available-memory query. See Driver.memoryQueryTimeout.
	defaultMemoryQueryTimeout = 15 * time.Second

	// vmStateNotFound is a sentinel returned by state-polling scripts when
	// the VM has disappeared (e.g. it finished tearing down on its own).
	vmStateNotFound = "__BOXY_NOT_FOUND__"

	// defaultHostReserveMB is headroom subtracted from the host's available
	// memory before it's offered to a new Create request, protecting the host OS and
	// other processes. Not currently user-configurable — see #173's design
	// spec, "Known gap surfaced by this work."
	defaultHostReserveMB = 512

	// deleteBestEffortAttempts/defaultDeleteBestEffortInterval bound
	// deleteBestEffort's cleanup retry: Remove-VM's -ErrorAction
	// SilentlyContinue masks whether it actually worked, so a single
	// attempt can silently leave a VM behind (see #174). Same order of
	// magnitude as defaultDeleteWaitInterval.
	deleteBestEffortAttempts        = 3
	defaultDeleteBestEffortInterval = 2 * time.Second

	// defaultCleanupTimeout bounds createFailure's cleanup, run on a context
	// detached from Create's caller (see createFailure's doc comment) —
	// generous enough for deleteBestEffortAttempts full retries plus real
	// PowerShell call latency, not just the retry-wait intervals between
	// them.
	defaultCleanupTimeout = 45 * time.Second

	// defaultReservationGraceInterval delays reserveMemory's release()
	// decrement past Create's return, biasing #183's under-/over-reservation
	// tradeoff toward the safe direction: a stale-high reservedMB can only
	// cause a spurious CapacityError on an immediately-following sequential
	// Create (annoying, safe), never let one overcommit the host
	// (dangerous). Applied uniformly to every release() — including
	// Create's failure path, where no VM ends up existing — rather than
	// only the success path: the same "annoying, safe" tradeoff holds
	// either way, and it's simpler than threading a second, ungraced
	// release variant through Create's failure branches. Same order of
	// magnitude as defaultDeleteWaitInterval.
	defaultReservationGraceInterval = 5 * time.Second
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

func (d *Driver) memQueryTimeout() time.Duration {
	if d.memoryQueryTimeout > 0 {
		return d.memoryQueryTimeout
	}
	return defaultMemoryQueryTimeout
}

func (d *Driver) bestEffortInterval() time.Duration {
	if d.deleteBestEffortInterval > 0 {
		return d.deleteBestEffortInterval
	}
	return defaultDeleteBestEffortInterval
}

func (d *Driver) gracePeriod() time.Duration {
	if d.reservationGraceInterval > 0 {
		return d.reservationGraceInterval
	}
	return defaultReservationGraceInterval
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
	} else if cc.MemoryMB < 0 {
		return nil, fmt.Errorf("config.memory_mb must be positive, got %d", cc.MemoryMB)
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

	release, err := d.reserveMemory(ctx, int64(cc.MemoryMB))
	if err != nil {
		return nil, err
	}
	releaseOnce := sync.OnceFunc(release)
	defer releaseOnce()

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
`,
		psq(cc.TemplateVHD),
		psq(diffPath),
		psq(vmName), cc.Generation, memBytes, psq(diffPath),
		switchBlock,
		psq(vmName), cc.CPUCount,
		psq(vmName), psq(notes),
		psq(vmName),
	)

	if _, err := d.ps(ctx, createScript); err != nil {
		return nil, d.createFailure(ctx, vmName, diffPath, fmt.Errorf("hyperv create VM %q: %w", vmName, err))
	}
	// Schedule the reservation's release now that Start-VM has committed the
	// memory, rather than waiting for Create to fully return. release()
	// defers the actual reservedMB decrement by a grace period (see
	// reserveMemory) — it does not free the reservation synchronously — but
	// calling it here still narrows the over-reservation window described in
	// #183: the window no longer includes the trailing (unguarded) ID
	// lookup below, only whatever grace period remains after it.
	releaseOnce()

	vmGUID, err := d.resolveCreatedVMID(ctx, vmName)
	if err != nil {
		// The VM is healthy and running — Start-VM already succeeded above.
		// Do NOT clean it up: that would destroy a good VM over a metadata
		// lookup hiccup. Leave it for the periodic ResourceLister sweep
		// (#174, Task 7) to pick up later.
		return nil, fmt.Errorf("hyperv create VM %q: resolve id: %w", vmName, err)
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

// List satisfies providersdk.ResourceLister, enumerating every boxy-*-named
// VM this driver's host currently has — including ones the store has no
// record of, e.g. left behind by a crash between New-VM succeeding and
// Create's failure branch running (see #174). Prefix-filtered inside the
// PowerShell query itself, not client-side, so a host running unrelated VMs
// alongside Boxy's never returns them to a caller that doesn't expect it.
func (d *Driver) List(ctx context.Context) ([]providersdk.ResourceStatus, error) {
	out, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
Get-VM | Where-Object { $_.Name -like 'boxy-*' } | ForEach-Object { "$($_.Id)|$($_.State)" }
`)
	if err != nil {
		return nil, fmt.Errorf("hyperv list: %w", err)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	statuses := make([]providersdk.ResourceStatus, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		statuses = append(statuses, providersdk.ResourceStatus{
			ID:    strings.TrimSpace(parts[0]),
			State: normalizeVMState(strings.TrimSpace(parts[1])),
		})
	}
	return statuses, nil
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

// ExecOp is retained as a provider-specific spelling of the shared command
// operation for compatibility with existing callers.
type ExecOp = providersdk.ExecOperation

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

// UpdateStream forwards a guest's native streaming capability. Hyper-V
// providers that cannot stream return an explicit capability error rather than
// buffering unary output and presenting it as live data.
func (d *Driver) UpdateStream(ctx context.Context, id string, op providersdk.Operation, sink eventstream.Sink) (*providersdk.Result, error) {
	execOp, ok := op.(*ExecOp)
	if !ok {
		return nil, fmt.Errorf("unsupported streaming operation type %T", op)
	}
	if len(execOp.Command) == 0 {
		return nil, fmt.Errorf("ExecOp.Command is empty")
	}
	if sink == nil {
		return nil, fmt.Errorf("stream sink is required")
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
		default:
			ge = psdirect.New(id, guestUser, guestPassword)
		}
	}

	streamer, ok := ge.(vmsdk.GuestExecStreamer)
	if !ok {
		return nil, fmt.Errorf("hyperv %s guest does not support streaming execution", guestOS)
	}
	result, err := streamer.ExecStream(ctx, execOp.Command[0], execOp.Command[1:], sink)
	if err != nil {
		return nil, fmt.Errorf("stream exec on %s guest (VM %s): %w", guestOS, id, err)
	}
	return &providersdk.Result{Outputs: map[string]string{
		"exit_code": strconv.Itoa(result.ExitCode),
	}}, nil
}

// --- Delete ---

// checkHostHealth runs a lightweight, VM-independent Hyper-V host probe
// before attempting to provision. If VMMS is already degraded (as can
// happen after a stuck teardown, see #118), this fails fast with a clear
// error instead of letting New-VHD/New-VM run into the same degraded state
// on every reconcile pass. Bounded by memQueryTimeout, which applies on top
// of (never beyond) the caller's ctx — so on the background reconcile
// ticker's ctx, which has no deadline of its own, a hung probe still fails
// Create promptly instead of hanging. This call precedes reserveMemory and
// doesn't hold d.mu, so unlike that call a hang here only blocks the
// current Create, not others.
func (d *Driver) checkHostHealth(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, d.memQueryTimeout())
	defer cancel()
	_, err := d.ps(probeCtx, `
$ErrorActionPreference = 'Stop'
Get-VMHost | Out-Null
'OK'
`)
	if err != nil {
		return fmt.Errorf("hyperv host probe (Get-VMHost) failed, VMMS may be degraded: %w", err)
	}
	return nil
}

// CapacityError is providersdk.CapacityError under this package's existing
// name — see #185's design spec for why the type moved.
type CapacityError = providersdk.CapacityError

// queryAvailableMemoryMB returns the host's current available physical
// memory in megabytes — memory immediately usable by a new VM, not just the
// raw free list. Win32_OperatingSystem.FreePhysicalMemory (the naive choice)
// excludes the standby/cache list, which Windows reclaims instantly under
// memory pressure; on a host that's been running a while that list can be
// several GB, so FreePhysicalMemory routinely underreports what's actually
// available and would spuriously reject Create requests Start-VM could
// satisfy fine. Win32_PerfFormattedData_PerfOS_Memory.AvailableMBytes is
// already in MB and matches what Task Manager calls "Available" — deliberately
// not Get-Counter '\Memory\Available MBytes', whose counter *path* is
// localized on non-English Windows.
func (d *Driver) queryAvailableMemoryMB(ctx context.Context) (int64, error) {
	out, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
(Get-CimInstance Win32_PerfFormattedData_PerfOS_Memory).AvailableMBytes
`)
	if err != nil {
		return 0, fmt.Errorf("hyperv query available memory: %w", err)
	}
	mb, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hyperv parse available memory %q: %w", out, err)
	}
	return mb, nil
}

// Availability implements providersdk.AvailabilityReporter.
func (d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error) {
	queryCtx, cancel := context.WithTimeout(ctx, d.memQueryTimeout())
	defer cancel()
	availableMB, err := d.queryAvailableMemoryMB(queryCtx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	reserved := d.reservedMB
	d.mu.Unlock()

	avail := availableMB - defaultHostReserveMB - reserved
	if avail < 0 {
		avail = 0
	}
	return &providersdk.ResourceAvailability{MemoryMB: avail}, nil
}

// reserveMemory atomically checks and commits requestedMB of host memory
// against live availability, returning a release closure that must be
// called exactly once (success or failure) to give the memory back. The
// mutex is held across the live PowerShell query itself, not just the
// accounting, closing the TOCTOU gap between concurrent Create calls on
// this driver instance — the primary case this guards is a host that can't
// fit even one more VM. It does NOT close the gap across sequential Create
// calls: release() runs as soon as Create returns, and the host's live
// available-memory counter isn't guaranteed to reflect a just-started VM's
// consumption by then, so a rapid pool fill can still overcommit. See
// #173's design spec, "Known gap", and the follow-up issue tracking a
// reservation model that ties release to VM deletion instead. The query
// itself is bounded by memQueryTimeout, which applies on top of (never
// beyond) ctx's own deadline — which may have none — so a hung PowerShell
// call can't hold this mutex — and therefore every other Create on this
// driver — indefinitely.
func (d *Driver) reserveMemory(ctx context.Context, requestedMB int64) (release func(), err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	queryCtx, cancel := context.WithTimeout(ctx, d.memQueryTimeout())
	defer cancel()
	availableMB, err := d.queryAvailableMemoryMB(queryCtx)
	if err != nil {
		return nil, err
	}

	available := availableMB - defaultHostReserveMB - d.reservedMB
	if available < requestedMB {
		// Clamp to 0 for the error message, matching Availability()'s clamp
		// for the same computation — a negative "available" (e.g. reservedMB
		// alone exceeding availableMB-reserve under load) is a confusing
		// thing to show a caller.
		reported := available
		if reported < 0 {
			reported = 0
		}
		return nil, &CapacityError{RequestedMemoryMB: requestedMB, AvailableMemoryMB: reported}
	}

	d.reservedMB += requestedMB
	return func() {
		// Independent of the caller's ctx (which may already be cancelled
		// by the time Create returns) — this is pure in-process bookkeeping,
		// not I/O, so it doesn't need one.
		time.AfterFunc(d.gracePeriod(), func() {
			d.mu.Lock()
			d.reservedMB -= requestedMB
			d.mu.Unlock()
		})
	}, nil
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

// deleteBestEffort attempts to remove a partially-created VM after Create
// fails, retrying up to deleteBestEffortAttempts times since a transient
// VMMS hiccup can make a single Stop-VM/Remove-VM attempt fail. Each attempt
// is followed by a Get-VM existence check — -ErrorAction SilentlyContinue on
// Remove-VM masks whether it actually worked, so this check is the only way
// to know for certain, and its own output doubles as the VM's GUID if
// cleanup still didn't work (needed to build OrphanedResourceError) — no
// separate lookup call added. Returns ("", nil) once the VM is confirmed
// gone. Returns (guid, err) if it's still present after all attempts; guid
// is empty only if every attempt's PowerShell call itself failed (host
// unreachable), meaning nothing could even be confirmed, let alone
// quarantined.
func (d *Driver) deleteBestEffort(ctx context.Context, vmName, vhdPath string) (guid string, err error) {
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
Stop-VM -Name '%s' -Force -TurnOff -ErrorAction SilentlyContinue
Remove-VM -Name '%s' -Force -ErrorAction SilentlyContinue
if ('%s' -ne '' -and (Test-Path '%s')) { Remove-Item '%s' -Force -ErrorAction SilentlyContinue }
$vm = Get-VM -Name '%s' -ErrorAction SilentlyContinue
if ($null -eq $vm) { '' } else { $vm.Id.ToString() }
`,
		psq(vmName), psq(vmName),
		psq(vhdPath), psq(vhdPath), psq(vhdPath),
		psq(vmName),
	)

	var lastErr error
	for attempt := 1; attempt <= deleteBestEffortAttempts; attempt++ {
		out, psErr := d.ps(ctx, script)
		if psErr != nil {
			lastErr = psErr
		} else if remaining := strings.TrimSpace(out); remaining == "" {
			return "", nil // confirmed gone
		} else {
			guid = remaining
			lastErr = fmt.Errorf("hyperv cleanup: VM %q still present after Remove-VM", vmName)
		}
		if attempt < deleteBestEffortAttempts {
			select {
			case <-ctx.Done():
				return guid, ctx.Err()
			case <-time.After(d.bestEffortInterval()):
			}
		}
	}
	return guid, lastErr
}

// resolveVMIDAttempts bounds resolveCreatedVMID's retry of a transient
// Get-VM hiccup. Its interval reuses d.bestEffortInterval() (see Task 3)
// rather than adding a third overridable interval field — both are "cheap
// bounded retry, 2s apart" by default, and tests already override the one
// field.
const resolveVMIDAttempts = 3

// resolveCreatedVMID looks up a just-started VM's GUID in a call separate
// from the create script (see #183's script split) — read-only, retried a
// few times for a transient Get-VM hiccup, never mutating anything, so a
// failure here never implies the VM itself is unhealthy.
func (d *Driver) resolveCreatedVMID(ctx context.Context, vmName string) (string, error) {
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
(Get-VM -Name '%s').Id.ToString()
`, psq(vmName))

	var lastErr error
	for attempt := 1; attempt <= resolveVMIDAttempts; attempt++ {
		out, err := d.ps(ctx, script)
		if err == nil {
			if guid := strings.TrimSpace(out); guid != "" {
				return guid, nil
			}
			lastErr = fmt.Errorf("empty VM GUID returned")
		} else {
			lastErr = err
		}
		if attempt < resolveVMIDAttempts {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(d.bestEffortInterval()):
			}
		}
	}
	return "", lastErr
}

// createFailure builds Create's return error after a failed create attempt,
// running best-effort cleanup and escalating to *providersdk.OrphanedResourceError
// (carrying the real GUID, resolved by deleteBestEffort's own existence
// check) when cleanup couldn't confirm the VM is gone. cause is the
// original failure that triggered cleanup.
//
// Cleanup runs on a context detached from Create's caller (a fresh
// context.Background, bounded by defaultCleanupTimeout) rather than the ctx
// Create was called with. If that ctx is already cancelled or past its
// deadline — the same ctx whose expiry may be exactly why createScript just
// failed — deleteBestEffort's first PowerShell call would fail immediately
// and its retry loop would return before ever running the existence check,
// leaving guid empty and this function silently returning cause as a plain
// error instead of *OrphanedResourceError: a VM that New-VM actually
// created goes untracked with no ID recorded anywhere. That's tolerable for
// AgentProvisioner (RemoteAgent), where the periodic ResourceLister sweep
// (#174, Task 7) eventually adopts it as an ordinary orphan — but
// DriverProvisioner has no such sweep at all, so for that deployment
// topology it would be permanently lost. Detaching cleanup from ctx costs
// Create extra latency on this one failure path in exchange for a real
// chance to confirm and quarantine — the same latency-for-safety trade this
// package already makes elsewhere (see ADR-0004's teardown guard).
func (d *Driver) createFailure(_ context.Context, vmName, diffPath string, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultCleanupTimeout)
	defer cancel()
	guid, cleanupErr := d.deleteBestEffort(cleanupCtx, vmName, diffPath)
	switch {
	case cleanupErr == nil:
		return cause
	case guid != "":
		return &providersdk.OrphanedResourceError{
			ID:           guid,
			CauseMessage: fmt.Sprintf("%v (cleanup also failed: %v)", cause, cleanupErr),
		}
	default:
		// No GUID to quarantine under — every deleteBestEffort attempt's
		// PowerShell call itself failed (e.g. host unreachable), rather than
		// confirming the VM still present. That's exactly the case where
		// cleanupErr matters most for diagnosing a possible orphan, so it
		// must not vanish silently the way returning bare cause would; %w
		// on both keeps errors.Is/As working over either chain.
		return fmt.Errorf("%w (cleanup also failed: %w)", cause, cleanupErr)
	}
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
