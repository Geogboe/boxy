package hyperv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/guestcred"
	"github.com/Geogboe/boxy/pkg/psdirect"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

var (
	_ providersdk.Driver            = (*Driver)(nil)
	_ providersdk.GuestPersonalizer = (*Driver)(nil)
	_ providersdk.NetworkIsolator   = (*Driver)(nil)
	_ providersdk.MeshPeerer        = (*Driver)(nil)
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

	// resolveBootstrap supplies a control-plane-owned bootstrap credential for
	// new VMs. A nil resolver preserves the explicit legacy env-ref fallback
	// for local development and older VMs.
	resolveBootstrap providersdk.GuestBootstrapResolver

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

	// hostReserveMB is host-wide headroom subtracted from available memory.
	// hostReserveConfigured distinguishes an explicit zero from an unconfigured
	// Driver built directly by older callers/tests.
	hostReserveMB         int64
	hostReserveConfigured bool

	// memoryBudgetMB caps aggregate startup memory assigned to Boxy-owned VMs.
	// Bare Drivers used by low-level tests remain unconfigured and retain the
	// legacy live-memory-only behavior; production Drivers built by New require
	// this value.
	memoryBudgetMB         int64
	memoryBudgetConfigured bool
	memoryRetryInterval    time.Duration

	// mu guards reservedMB, the memory (in MB) committed to in-flight Create
	// calls that a live host query doesn't reflect yet. See reserveMemory.
	mu         sync.Mutex
	reservedMB int64

	// personalizeLocksMu guards personalizeLocks, one per-resource mutex per
	// VM ID currently (or previously) personalizing. PersonalizeGuest is
	// called at least twice per resource (preheat and allocation) and any
	// retry can overlap either of those; without a per-resource lease,
	// concurrent invocations each open an independent PowerShell Direct
	// session and race the same clear-then-reapply network script and
	// password rotation against the same guest, which can strand a VM on an
	// APIPA address with its original bootstrap password (#336). This
	// mirrors internal/pool.Manager.lockPool's per-key mutex-map pattern.
	personalizeLocksMu sync.Mutex
	personalizeLocks   map[string]*sync.Mutex

	// rotatedCredsMu guards rotatedCreds, the in-memory record of the
	// credential personalizeGuestLocked most recently rotated a guest onto.
	// See rememberRotatedCredential for why this exists and why it is not a
	// violation of ADR-0010.
	rotatedCredsMu sync.Mutex
	rotatedCreds   map[string]rotatedGuestCredential

	// segmentLedgerPath is where the per-sandbox network-segment CIDR
	// ledger is persisted (see network_isolation.go). New resolves it under
	// Config.DataDir when a boxy config file's directory is known
	// (RelativePathResolver). Empty falls back to a per-Driver ephemeral temp
	// location — see segments().
	segmentLedgerPath string

	// segmentLedger/segmentLedgerOnce cache the *segmentLedger instance so
	// every CreateSegment/AttachToSegment/DestroySegment call on this Driver
	// shares one diskjson.Store — and therefore one sync.Mutex — over
	// segmentLedgerFilename. Building a fresh *segmentLedger (and fresh,
	// unshared mutex) per call would defeat the ledger's own concurrency
	// guarantee, letting two concurrent allocate() calls each read a stale
	// snapshot and race their writes (task-2 code review finding 1).
	segmentLedger     *segmentLedger
	segmentLedgerOnce sync.Once

	// segmentHostMu serializes the host-side PowerShell of CreateSegment and
	// DestroySegment. Hyper-V fails concurrent Internal switch creation
	// ("Adding ports to the switch ... failed", error 32790, seen on wks01
	// 2026-09-24 with two concurrent creates), so segment changes on one
	// host run one at a time.
	segmentHostMu sync.Mutex

	// meshEndpoint is Config.MeshEndpoint, threaded through the same way
	// segmentLedgerPath is.
	meshEndpoint string

	// meshInterfaces holds this driver's live mesh interfaces per segment,
	// created lazily on first MeshIdentity call. In-memory only -- a process
	// restart loses these (and any peer must reconnect, which is expected
	// WireGuard behavior after any endpoint goes down, not something this
	// driver needs to special-case).
	meshMu         sync.Mutex
	meshInterfaces map[providersdk.SegmentRef]meshInterface

	// newMeshInterface is the mesh interface factory, keyed only by ifName
	// (always listenPort 0 -- an ephemeral port is fine for a mesh peer).
	// nil in production, which meshInterfaceFor resolves to meshnet.New;
	// tests inject a fake here to avoid needing a real OS TUN device and an
	// actual WireGuard handshake.
	newMeshInterface func(ifName string) (meshInterface, error)
}

// lockPersonalize serializes PersonalizeGuest invocations for the same VM
// ID. See personalizeLocks for why this is required.
func (d *Driver) lockPersonalize(id string) func() {
	d.personalizeLocksMu.Lock()
	if d.personalizeLocks == nil {
		d.personalizeLocks = make(map[string]*sync.Mutex)
	}
	lock := d.personalizeLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		d.personalizeLocks[id] = lock
	}
	d.personalizeLocksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// forgetPersonalizeLock drops id's entry from personalizeLocks -- and its
// retained rotated credential -- once Delete has confirmed the VM gone, so
// neither map grows unboundedly over a long-running daemon's lifetime as
// pools recycle resources. Safe to call even if no lock or credential was
// ever recorded for id.
//
// The credential is cleared here, at the resource's end of life, rather than
// at its last known consumer (a successful AttachToSegment): the sandbox
// allocation loop can re-run AttachToSegment for an already-attached
// resource when a *later* resource in the same sandbox fails, and dropping
// the credential on first success would leave that retry with only the stale
// pool bootstrap to authenticate with. One cleanup site tied to Delete is
// also one lifecycle to reason about rather than two.
func (d *Driver) forgetPersonalizeLock(id string) {
	d.personalizeLocksMu.Lock()
	delete(d.personalizeLocks, id)
	d.personalizeLocksMu.Unlock()

	d.rotatedCredsMu.Lock()
	delete(d.rotatedCreds, id)
	d.rotatedCredsMu.Unlock()
}

// rotatedGuestCredential is the credential a guest was most recently rotated
// onto by this driver, held in memory only.
type rotatedGuestCredential struct {
	username string
	password string
}

// rememberRotatedCredential records the credential personalizeGuestLocked
// just rotated id's guest onto, so a later same-process guest-exec step for
// the same resource can still authenticate.
//
// This exists because of a real ordering gap found implementing #224's Plan
// 1c. AttachToSegment must run an in-guest command (assignGuestIP) at
// allocation time, and by then the guest's password is neither of the values
// the driver can look up: PersonalizeGuest rotated it off the pool bootstrap
// at admission, and internal/pool's allocation path deletes the server-side
// per-resource credential immediately after the allocation-time rotation,
// one-time-delivering the new value to the sandbox caller instead (ADR-0010).
// So resolveBootstrapCredential at attach time returns the *pool* bootstrap,
// which the guest was rotated off long before. The driver that generated the
// password is the only party that still has it, and it is the same driver
// that needs it: ProviderRef.AgentID routes Allocate and AttachToSegment for
// one resource back to the same agent process.
//
// This does not widen ADR-0010's boundary. The rotated credential is already
// described there as opaque and process-local; this value never reaches
// model.Resource.Properties, VM notes, logs, the API, or remote agent
// configuration, and is dropped when the resource is deleted
// (forgetPersonalizeLock).
//
// Called only after verify_credential succeeds -- an unverified rotation is
// a password the guest cannot be proven to have accepted, and caching it
// would mean confidently authenticating with something wrong -- and only for
// an allocation-time personalization (opts.ApplyNetwork). Admission-time
// preheat rotations are deliberately not retained: nothing will attach those
// resources to a segment until a sandbox claims them, so holding their
// passwords would mean this process kept the plaintext credential of every
// idle VM in every Hyper-V pool, indefinitely, to serve a call that may never
// come. Retention is scoped to in-flight allocations, which is the only
// window AttachToSegment occupies.
func (d *Driver) rememberRotatedCredential(id, username, password string) {
	d.rotatedCredsMu.Lock()
	defer d.rotatedCredsMu.Unlock()
	if d.rotatedCreds == nil {
		d.rotatedCreds = make(map[string]rotatedGuestCredential)
	}
	d.rotatedCreds[id] = rotatedGuestCredential{username: username, password: password}
}

// segmentGuestCredential resolves the credential AttachToSegment should use
// to reach id's guest, preferring the credential this process rotated the
// guest onto and falling back to resolveBootstrapCredential.
//
// The fallback is genuinely best-effort, not a second reliable path: it is
// correct only for a guest that has never been rotated (so the pool
// bootstrap is still current), and is reached in practice when this agent
// restarted between allocation and attach, taking its in-memory record with
// it. Keeping it costs nothing and turns one narrow crash window from a
// certain failure into a possible success.
//
// When it does fail it fails loudly, but not cheaply: the authentication
// error propagates out of AttachToSegment, through
// internal/sandbox.Manager.ensureNetworkSegment as a hard error, and fails
// the allocation -- the fulfiller then rolls the sandbox back to `failed` and
// quarantines the resource. That is still strictly better than a silently
// mis-addressed guest, which is why the fallback stays, but it is a failed
// sandbox, not merely an unaddressed VM.
func (d *Driver) segmentGuestCredential(ctx context.Context, id string, notes map[string]string, guestUser string) (string, string, error) {
	d.rotatedCredsMu.Lock()
	rotated, ok := d.rotatedCreds[id]
	d.rotatedCredsMu.Unlock()
	if ok {
		username := rotated.username
		if strings.TrimSpace(username) == "" {
			username = guestUser
		}
		return username, rotated.password, nil
	}

	bootstrap, err := d.resolveBootstrapCredential(ctx, id, notes, guestUser)
	if err != nil {
		return "", "", fmt.Errorf("resolve guest credential: %w", err)
	}
	username := bootstrap.Username
	if strings.TrimSpace(username) == "" {
		username = guestUser
	}
	return username, bootstrap.Password, nil
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
	defaultMemoryQueryTimeout  = 15 * time.Second
	defaultMemoryRetryInterval = 250 * time.Millisecond
	memoryProbeAttempts        = 3

	// vmStateNotFound is a sentinel returned by state-polling scripts when
	// the VM has disappeared (e.g. it finished tearing down on its own).
	vmStateNotFound = "__BOXY_NOT_FOUND__"

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

func (d *Driver) hostReserve() int64 {
	if !d.hostReserveConfigured {
		return DefaultHostReserveMB
	}
	return d.hostReserveMB
}

func (d *Driver) memoryBudget() int64 {
	if !d.memoryBudgetConfigured {
		return 0
	}
	return d.memoryBudgetMB
}

func (d *Driver) memoryRetryDelay() time.Duration {
	if d.memoryRetryInterval > 0 {
		return d.memoryRetryInterval
	}
	return defaultMemoryRetryInterval
}

// New creates a Hyper-V driver and validates its host-wide configuration.
func New(cfg *Config) (*Driver, error) {
	reserve, err := cfg.effectiveHostReserveMB()
	if err != nil {
		return nil, err
	}
	budget, err := cfg.effectiveMemoryBudgetMB()
	if err != nil {
		return nil, err
	}

	dataDir := ""
	if cfg != nil {
		dataDir = cfg.DataDir
	}
	if dataDir == "" {
		dataDir = filepath.Join(defaultDataDirBase, defaultDataDirHyperV)
	}
	if !filepath.IsAbs(dataDir) {
		// Only reached when cfg.DataDir was never anchored by
		// ResolveRelativePaths — i.e. no boxy config file was known to the
		// caller. Documented, narrower gap: see ADR-0012's "Ledger
		// location: Config.DataDir".
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve hyperv data dir: %w", err)
		}
		dataDir = filepath.Join(wd, dataDir)
	}

	meshEndpoint := ""
	if cfg != nil {
		meshEndpoint = cfg.MeshEndpoint
	}

	return &Driver{
		hostReserveMB:          reserve,
		hostReserveConfigured:  true,
		memoryBudgetMB:         budget,
		memoryBudgetConfigured: true,
		segmentLedgerPath:      filepath.Join(dataDir, segmentLedgerFilename),
		meshEndpoint:           meshEndpoint,
	}, nil
}

// SetGuestBootstrapResolver injects the control-plane lookup used for new
// VMs. The callback is evaluated at personalization time so remote-agent
// reconnects always use their current gRPC connection and the server's
// current pool credential.
func (d *Driver) SetGuestBootstrapResolver(resolver providersdk.GuestBootstrapResolver) {
	d.resolveBootstrap = resolver
}

func (d *Driver) Type() providersdk.Type { return ProviderType }

// --- Create ---

func (d *Driver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	cc, err := decodeCreateConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("decode create config: %w", err)
	}
	if cc.Source != nil {
		if strings.TrimSpace(cc.TemplateVHD) != "" {
			return nil, fmt.Errorf("config.template_vhd and config.source are mutually exclusive")
		}
		templateVHD, sourceErr := materializeSource(ctx, cc.Source, cc.VHDDir)
		if sourceErr != nil {
			return nil, fmt.Errorf("ingest source: %w", sourceErr)
		}
		cc.TemplateVHD = templateVHD
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

	if err := d.checkTemplateNotAttached(ctx, cc.TemplateVHD); err != nil {
		return nil, err
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

	// Store only non-sensitive Boxy guest metadata in VM Notes. Bootstrap and
	// rotated credentials are delivered out-of-band and never written to the VM.
	//
	// No network fields are written here any more: a claimed VM's address
	// comes from its sandbox's segment ledger entry at attach time, not from
	// anything recorded per-VM at Create time. See AttachToSegment.
	notes := fmt.Sprintf("boxy_guest_os=%s;boxy_guest_user=%s", cc.GuestOS, guestUser)

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
			"vm_name":    vmName,
			"vm_id":      vmGUID,
			"guest_os":   cc.GuestOS,
			"guest_user": guestUser,
		},
	}, nil
}

func materializeSource(ctx context.Context, source *providersdk.SourceDescriptor, destinationDir string) (string, error) {
	if source == nil {
		return "", fmt.Errorf("source is nil")
	}
	if err := source.Validate(); err != nil {
		return "", err
	}
	format := strings.ToLower(strings.TrimSpace(source.Format))
	var extension string
	switch format {
	case "vhd", "hyperv-vhd":
		extension = ".vhd"
	case "vhdx", "hyperv-vhdx":
		extension = ".vhdx"
	default:
		return "", fmt.Errorf("unsupported source format %q; expected vhd or vhdx", source.Format)
	}
	digest := strings.TrimPrefix(strings.ToLower(source.Digest), "sha256:")
	if destinationDir == "" {
		destinationDir = filepath.Join(os.TempDir(), "boxy-source-cache")
	}
	path := filepath.Join(destinationDir, "boxy-source-"+digest+extension)
	if err := providersdk.PullSource(ctx, *source, path); err != nil {
		return "", err
	}
	return path, nil
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
//
// The result is deliberately one compact JSON value rather than a
// newline-delimited string. PowerShell Direct/PSRP can remove line endings
// from a multiline string, which turns a complete multi-VM listing into a
// partial listing. A malformed payload is an error: callers must not mistake
// an incomplete snapshot for authoritative absence and reap valid resources.
func (d *Driver) List(ctx context.Context) ([]providersdk.ResourceStatus, error) {
	out, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
$items = @(Get-VM | Where-Object { $_.Name -like 'boxy-*' } | ForEach-Object {
    [pscustomobject]@{ id = $_.Id.ToString(); state = $_.State.ToString() }
})
ConvertTo-Json -InputObject $items -Compress
`)
	if err != nil {
		return nil, fmt.Errorf("hyperv list: %w", err)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}

	var records []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(trimmed), &records); err != nil {
		return nil, fmt.Errorf("hyperv list: decode JSON: %w", err)
	}
	statuses := make([]providersdk.ResourceStatus, 0, len(records))
	for i, record := range records {
		if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.State) == "" {
			return nil, fmt.Errorf("hyperv list: record %d has empty id or state", i)
		}
		statuses = append(statuses, providersdk.ResourceStatus{
			ID:    strings.TrimSpace(record.ID),
			State: normalizeVMState(strings.TrimSpace(record.State)),
		})
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
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
	if op.Script == nil && len(op.Command) == 0 && op.CommandText == "" {
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
	guestUser, guestPassword, err := decodeGuestPassword(op.GuestCredential, guestUser)
	if err != nil {
		return nil, fmt.Errorf("decode guest credential for %s: %w", id, err)
	}

	ge, err := d.newGuestExec(ctx, id, guestOS, guestUser, guestPassword, "")
	if err != nil {
		return nil, err
	}
	if op.Script != nil {
		interpreter, err := scriptInterpreterForGuest(op.Script.Interpreter, guestOS)
		if err != nil {
			return nil, err
		}
		path, err := stageHyperVScript(ctx, ge, op.Script, guestOS)
		if err != nil {
			return nil, fmt.Errorf("stage script on %s guest (VM %s): %w", guestOS, id, err)
		}
		cmd, args := hyperVScriptCommand(interpreter, path, op.Script.Args)
		result, err := ge.Exec(ctx, cmd, args...)
		if err != nil {
			return nil, fmt.Errorf("execute script on %s guest (VM %s): %w", guestOS, id, err)
		}
		return &providersdk.Result{Outputs: map[string]string{
			"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": strconv.Itoa(result.ExitCode),
		}}, nil
	}

	if op.CommandText != "" {
		textExec, ok := ge.(vmsdk.GuestExecText)
		if !ok {
			return nil, fmt.Errorf("%s guest does not support opaque command text", guestOS)
		}
		result, err := textExec.ExecText(ctx, op.CommandText)
		if err != nil {
			return nil, fmt.Errorf("exec text on %s guest (VM %s): %w", guestOS, id, err)
		}
		return &providersdk.Result{Outputs: map[string]string{
			"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": strconv.Itoa(result.ExitCode),
		}}, nil
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
	if execOp.Script == nil && len(execOp.Command) == 0 && execOp.CommandText == "" {
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
	guestUser, guestPassword, err := decodeGuestPassword(execOp.GuestCredential, guestUser)
	if err != nil {
		return nil, fmt.Errorf("decode guest credential for %s: %w", id, err)
	}

	ge, err := d.newGuestExec(ctx, id, guestOS, guestUser, guestPassword, "")
	if err != nil {
		return nil, err
	}

	streamer, ok := ge.(vmsdk.GuestExecStreamer)
	if !ok {
		return nil, fmt.Errorf("hyperv %s guest does not support streaming execution", guestOS)
	}
	if execOp.Script != nil {
		interpreter, err := scriptInterpreterForGuest(execOp.Script.Interpreter, guestOS)
		if err != nil {
			return nil, err
		}
		path, err := stageHyperVScript(ctx, ge, execOp.Script, guestOS)
		if err != nil {
			return nil, fmt.Errorf("stage script on %s guest (VM %s): %w", guestOS, id, err)
		}
		cmd, args := hyperVScriptCommand(interpreter, path, execOp.Script.Args)
		result, err := streamer.ExecStream(ctx, cmd, args, sink)
		if err != nil {
			return nil, fmt.Errorf("stream script on %s guest (VM %s): %w", guestOS, id, err)
		}
		return &providersdk.Result{Outputs: map[string]string{"exit_code": strconv.Itoa(result.ExitCode)}}, nil
	}
	if execOp.CommandText != "" {
		textStreamer, ok := ge.(vmsdk.GuestExecStreamText)
		if !ok {
			return nil, fmt.Errorf("%s guest does not support opaque command text streaming", guestOS)
		}
		result, err := textStreamer.ExecStreamText(ctx, execOp.CommandText, sink)
		if err != nil {
			return nil, fmt.Errorf("stream exec text on %s guest (VM %s): %w", guestOS, id, err)
		}
		return &providersdk.Result{Outputs: map[string]string{
			"exit_code": strconv.Itoa(result.ExitCode),
		}}, nil
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

// checkTemplateNotAttached refuses to clone a template VHD that some VM is
// directly using as its own attached disk right now. Cloning (New-VHD
// -Differencing) or copying the backing file of a disk a live VM is
// actively writing to produces a torn/inconsistent child image -- reproduced
// on real hardware (wks01, 2026-09-16): every child cloned while the
// template VM was running failed to boot an operating system (Hyper-V
// Worker-Admin event 18603) and never established any Hyper-V
// integration-service contact, which surfaced many steps downstream as a
// PersonalizeGuest/HvSocket connect failure with nothing pointing back at
// the actual cause. Stopping the template VM before cloning fixed it
// outright.
//
// This deliberately checks Get-VMHardDiskDrive for a *running* VM whose disk
// path is exactly templateVHD, not Get-VHD's Attached property: Attached is
// true whenever *any* differencing child of templateVHD is running too,
// since a running child holds a read handle on its parent for on-demand
// block reads -- an expected, safe, core use of differencing disks, and not
// a second pool's clone attempt racing this check into a false positive on
// wks01 (reproduced 2026-09-16: two pools sharing one template, the second
// pool's clone permanently blocked by the first pool's own healthy running
// resource). The State filter matters just as much: Get-VMHardDiskDrive
// returns a VM's *configured* disk regardless of power state, so without it
// the template's own VM would always match this path check, running or not
// -- also reproduced on wks01, immediately after fixing the Attached false
// positive above, before the template VM was ever restarted. Only a running
// VM using templateVHD as its own disk -- not as a parent, not merely
// configured to use it while stopped -- is unsafe to clone from.
func (d *Driver) checkTemplateNotAttached(ctx context.Context, templateVHD string) error {
	probeCtx, cancel := context.WithTimeout(ctx, d.memQueryTimeout())
	defer cancel()
	out, err := d.ps(probeCtx, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$inUse = Get-VM | Where-Object { $_.State -eq 'Running' } | Get-VMHardDiskDrive | Where-Object { $_.Path -ieq '%s' }
if ($inUse) { 'True' } else { 'False' }
`, psq(templateVHD)))
	if err != nil {
		return fmt.Errorf("hyperv check template_vhd %q attachment: %w", templateVHD, err)
	}
	if strings.EqualFold(strings.TrimSpace(out), "True") {
		return fmt.Errorf("hyperv template_vhd %q is currently attached to a running VM; "+
			"stop that VM before using its disk as a clone source -- cloning a live disk "+
			"produces a torn child image that cannot boot", templateVHD)
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
//
// The perf-counter WMI class itself is not always available: real hosts can
// have a corrupted performance-counter registration (`lodctr /R` territory)
// that makes Get-CimInstance fail with "Invalid class" (WBEM_E_INVALID_CLASS)
// even though the host is otherwise healthy — reproduced on real hardware
// (wks01, 2026-09-16) with Get-Counter failing identically, ruling out just
// switching to it as the fallback. GlobalMemoryStatusEx (kernel32, via a
// P/Invoke) reports the same "available" semantics (it's what Task Manager's
// own figure is ultimately sourced from) without going through any WMI
// performance-counter provider, so it's the fallback when the perf class
// errors, not a second WMI query.
func (d *Driver) queryAvailableMemoryMB(ctx context.Context) (int64, error) {
	out, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
try {
    (Get-CimInstance Win32_PerfFormattedData_PerfOS_Memory).AvailableMBytes
} catch {
    $src = @'
using System;
using System.Runtime.InteropServices;
public class BoxyMemInfo {
    [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Auto)]
    public struct MEMORYSTATUSEX {
        public uint dwLength;
        public uint dwMemoryLoad;
        public ulong ullTotalPhys;
        public ulong ullAvailPhys;
        public ulong ullTotalPageFile;
        public ulong ullAvailPageFile;
        public ulong ullTotalVirtual;
        public ulong ullAvailVirtual;
        public ulong ullAvailExtendedVirtual;
    }
    [DllImport("kernel32.dll", SetLastError=true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool GlobalMemoryStatusEx(ref MEMORYSTATUSEX lpBuffer);
    public static ulong GetAvailablePhysicalMB() {
        MEMORYSTATUSEX mem = new MEMORYSTATUSEX();
        mem.dwLength = (uint)Marshal.SizeOf(typeof(MEMORYSTATUSEX));
        if (!GlobalMemoryStatusEx(ref mem)) {
            throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
        }
        return mem.ullAvailPhys / (1024UL * 1024UL);
    }
}
'@
    Add-Type -TypeDefinition $src -Language CSharp
    [BoxyMemInfo]::GetAvailablePhysicalMB()
}
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

func (d *Driver) queryAvailableMemoryWithRetry(ctx context.Context) (int64, error) {
	var lastErr error
	for attempt := 1; attempt <= memoryProbeAttempts; attempt++ {
		available, err := d.queryAvailableMemoryMB(ctx)
		if err == nil {
			return available, nil
		}
		lastErr = err
		if attempt == memoryProbeAttempts {
			break
		}
		timer := time.NewTimer(d.memoryRetryDelay())
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
	return 0, lastErr
}

// queryBoxyMemoryMB returns aggregate startup memory for every Boxy-owned VM
// visible on the provider host. Querying the host makes the budget survive
// process restarts and includes VMs created by another Boxy process.
func (d *Driver) queryBoxyMemoryMB(ctx context.Context) (int64, error) {
	out, err := d.ps(ctx, `
$ErrorActionPreference = 'Stop'
$sum = (Get-VM -Name 'boxy-*' -ErrorAction SilentlyContinue | Measure-Object -Property MemoryStartup -Sum).Sum
if ($null -eq $sum) { 0 } else { [math]::Floor([double]$sum / 1MB) }
`)
	if err != nil {
		return 0, fmt.Errorf("hyperv query Boxy VM memory: %w", err)
	}
	mb, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hyperv parse Boxy VM memory %q: %w", out, err)
	}
	return mb, nil
}

// Availability implements providersdk.AvailabilityReporter.
func (d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error) {
	queryCtx, cancel := context.WithTimeout(ctx, d.memQueryTimeout())
	defer cancel()
	availableMB, err := d.queryAvailableMemoryWithRetry(queryCtx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	reserved := d.reservedMB

	liveRemaining := clampMemory(availableMB - d.hostReserve() - reserved)
	remaining := liveRemaining
	if d.memoryBudgetConfigured {
		used, err := d.queryBoxyMemoryMB(queryCtx)
		if err != nil {
			return nil, err
		}
		budgetRemaining := clampMemory(d.memoryBudgetMB - used - reserved)
		if budgetRemaining < remaining {
			remaining = budgetRemaining
		}
	}
	return &providersdk.ResourceAvailability{MemoryMB: remaining}, nil
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
	availableMB, err := d.queryAvailableMemoryWithRetry(queryCtx)
	if err != nil {
		return nil, err
	}

	available := clampMemory(availableMB - d.hostReserve() - d.reservedMB)
	if d.memoryBudgetConfigured {
		used, err := d.queryBoxyMemoryMB(queryCtx)
		if err != nil {
			return nil, err
		}
		budgetRemaining := clampMemory(d.memoryBudgetMB - used - d.reservedMB)
		if budgetRemaining < available {
			available = budgetRemaining
		}
	}
	if available < requestedMB {
		logHyperVEvent(slog.LevelWarn, "hyperv memory admission refused", "admission", "memory_reserve", "failed", "", "insufficient_memory")
		return nil, &CapacityError{RequestedMemoryMB: requestedMB, AvailableMemoryMB: available}
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

// logHyperVEvent emits a structured diagnostics event for a Hyper-V
// admission/personalization phase. component/provider/operation/step/
// status/resource/error_code are all attribute keys
// pkg/diagnostics/handler.go's safeField recognizes, so on an agent that has
// installed a diagnostics.Handler as its slog default (see boxy serve /
// boxy agent serve), this reaches the diagnostics store with no direct
// dependency on pkg/diagnostics from this package. resourceID may be empty
// for a phase that runs before a VM exists (e.g. the memory preflight).
func logHyperVEvent(level slog.Level, msg, operation, step, status, resourceID, errorCode string) {
	attrs := []any{
		"component", "hyperv", "provider", "hyperv",
		"operation", operation, "step", step, "status", status,
	}
	if resourceID != "" {
		attrs = append(attrs, "resource", resourceID)
	}
	if errorCode != "" {
		attrs = append(attrs, "error_code", errorCode)
	}
	slog.Log(context.Background(), level, msg, attrs...)
}

func clampMemory(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
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

func (d *Driver) Delete(ctx context.Context, id string) (err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("resource id is required")
	}

	// Drop id's per-resource in-memory state (its personalize lock and the
	// guest credential this driver rotated it onto) whenever Delete confirms
	// the VM gone or successfully removes it — its two nil-return paths
	// below. Deferred so both paths clean up without duplicating the call,
	// and so a non-nil return (e.g. ErrVMBusy) does NOT: the VM may still be
	// alive and reachable with that credential, and a later retry of Delete
	// (recycle backoff, drain, the orphan sweep) will reach a nil return and
	// clean up then.
	defer func() {
		if err == nil {
			d.forgetPersonalizeLock(id)
		}
	}()

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
	result, err := d.PersonalizeGuest(ctx, id, providersdk.GuestPersonalizationOptions{ApplyNetwork: true})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.AccessDetails.ToProperties(), nil
}

// PersonalizeGuest rotates the guest's admin credential for the VM
// identified by id and reports whatever address that VM currently holds. It
// holds a per-resource lock for the duration (see lockPersonalize) so that
// overlapping invocations for the same VM — admission and allocation both
// call this, and either can retry — cannot interleave their PowerShell
// Direct sessions against the same guest. Failures emit a structured event
// distinguishing which phase failed (see personalizeFailureStep) rather than
// a single undifferentiated bucket.
//
// This driver no longer applies any network configuration of its own (#224,
// Plan 1c): a pool's declared static_ip/range config was removed, and a
// claimed VM is addressed from its sandbox's network segment by
// AttachToSegment instead. Rotation and verification use PowerShell Direct
// over VMBus, which needs no network, so they run for an unclaimed preheated
// VM exactly as before — #358's principle (a preheated-but-unclaimed VM must
// not become network-reachable early) now holds for free, since nothing
// reachable is configured until a sandbox claims it.
//
// opts.ApplyNetwork still selects the phase, and gates one thing here: whether
// the rotated credential is retained in memory for a subsequent
// AttachToSegment (see rememberRotatedCredential). Only an allocation-time
// call has an attach coming; an admission-time one must not leave a preheated
// VM's password resident for the pool's whole preheat lifetime. See ADR-0021's
// 2026-09-14 change-log entry.
func (d *Driver) PersonalizeGuest(ctx context.Context, id string, opts providersdk.GuestPersonalizationOptions) (*providersdk.GuestPersonalizationResult, error) {
	start := time.Now()
	unlock := d.lockPersonalize(id)
	defer unlock()
	result, err := d.personalizeGuestLocked(ctx, id, opts)
	elapsed := time.Since(start)
	if err != nil {
		step := personalizeFailureStep(err)
		slog.Warn(fmt.Sprintf("hyperv guest personalization failed after %s (step=%s)", elapsed, step),
			"component", "hyperv", "provider", "hyperv",
			"operation", "personalize", "step", step, "status", "failed",
			"resource", id, "error_code", step+"_failed",
			"elapsed_ms", elapsed.Milliseconds())
		return nil, err
	}
	slog.Info(fmt.Sprintf("hyperv guest personalization succeeded in %s", elapsed),
		"component", "hyperv", "provider", "hyperv",
		"operation", "personalize", "step", "guest_personalize", "status", "succeeded",
		"resource", id, "elapsed_ms", elapsed.Milliseconds())
	return result, nil
}

// personalizeStepTimer logs each major phase of guest personalization at
// info level with its own elapsed duration, so an operator watching normal
// (non-error, non-timeout) allocations can see exactly where time goes
// instead of only learning about a step after it fails or times out (#355).
// This is deliberately independent of the pool-reconcile PolicyController's
// "policy decision is noop" logging (pkg/policycontroller/controller.go),
// which reflects an entirely separate periodic loop and carries no
// information about an in-flight allocation/personalize call — see #355's
// investigation notes.
type personalizeStepTimer struct {
	id       string
	total    time.Time
	stepFrom time.Time
}

func newPersonalizeStepTimer(id string) *personalizeStepTimer {
	now := time.Now()
	return &personalizeStepTimer{id: id, total: now, stepFrom: now}
}

// step logs the elapsed time since the previous step (or the timer's
// creation) attributed to the named phase, then resets the clock for the
// next step.
func (t *personalizeStepTimer) step(name string) {
	now := time.Now()
	stepElapsed := now.Sub(t.stepFrom)
	totalElapsed := now.Sub(t.total)
	// The elapsed values are also embedded in the message text (not just
	// the structured attrs) so they remain visible in `boxy diagnostics
	// logs`'s default table view, which prints only timestamp/level/
	// component/message and not arbitrary attrs (#355).
	slog.Info(fmt.Sprintf("hyperv guest personalization step %q took %s (%s elapsed total)", name, stepElapsed, totalElapsed),
		"component", "hyperv", "provider", "hyperv",
		"operation", "personalize", "step", name,
		"resource", t.id,
		"elapsed_ms", stepElapsed.Milliseconds(),
		"total_elapsed_ms", totalElapsed.Milliseconds())
	t.stepFrom = now
}

// personalizeFailureStep classifies a personalizeGuestLocked failure by
// which phase it came from, so diagnostics distinguish "rotation failed"
// from "verification failed" instead of one generic bucket (see #336's
// suggested fix direction).
//
// The "network_apply" bucket is gone along with the in-guest addressing this
// method used to do: the address read that replaced it no longer fails the
// call at all (it warns and reports no address), and segment addressing
// happens in AttachToSegment, which reports its own errors on the allocation
// path rather than through this classifier.
func personalizeFailureStep(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "rotate guest credential"):
		return "rotate"
	case strings.Contains(msg, "verify rotated guest credential"), strings.Contains(msg, "reconnect with rotated guest credential"):
		return "verify"
	case strings.Contains(msg, "resolve guest bootstrap credential"), strings.Contains(msg, "generate guest credential"),
		strings.Contains(msg, "resolve VM name"), strings.Contains(msg, "read VM notes"):
		return "prepare"
	default:
		return "guest_personalize"
	}
}

// personalizeGuestLocked is PersonalizeGuest's implementation, run only
// while the caller holds this VM's personalize lock.
//
// opts.ApplyNetwork no longer gates any in-guest network configuration —
// this driver applies none of its own any more (#224, Plan 1c) — but it is
// still what distinguishes an allocation-time call from an admission-time
// one, and that distinction decides whether the rotated credential is
// retained for a subsequent AttachToSegment. See the retention call at the
// end of this function.
func (d *Driver) personalizeGuestLocked(ctx context.Context, id string, opts providersdk.GuestPersonalizationOptions) (*providersdk.GuestPersonalizationResult, error) {
	timer := newPersonalizeStepTimer(id)

	notes, err := d.readNotes(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read VM notes for %s: %w", id, err)
	}
	timer.step("read_notes")

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
	bootstrap, err := d.resolveBootstrapCredential(ctx, id, notes, guestUser)
	if err != nil {
		return nil, fmt.Errorf("resolve guest bootstrap credential for %s: %w", id, err)
	}
	if strings.TrimSpace(bootstrap.Username) != "" {
		guestUser = bootstrap.Username
	}
	timer.step("resolve_bootstrap_credential")

	newPassword, err := guestcred.GenerateRandomPassword()
	if err != nil {
		return nil, fmt.Errorf("generate guest credential for %s: %w", id, err)
	}

	vmName, err := d.vmNameFromID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("resolve VM name for %s: %w", id, err)
	}

	// PersonalizeGuest no longer applies any address of its own. A pool's
	// declared static_ip/range config was removed with #224's Plan 1c: every
	// isolation-capable Hyper-V pool gets a per-sandbox segment at allocation
	// time, and AttachToSegment addresses the guest from that segment's own
	// block, so anything applied here could only ever be superseded. What
	// remains is an observation — the address the VM currently holds on the
	// pool's switch, from DHCP or a pre-baked image — reported so a resource
	// that is never claimed still advertises however it is reachable today.
	//
	// That value is provisional for a claimed resource: AttachToSegment
	// replaces it. opts.ApplyNetwork is therefore no longer consulted here;
	// reading an address Hyper-V already assigned is not "applying"
	// boxy-managed configuration, which is exactly the distinction #358 drew
	// for DHCP-mode resources, now the only mode this driver has.
	//
	// A VM with no readable address is no longer a hard failure. Before Plan
	// 1c a pool could declare its own address and skip this read entirely;
	// with that config gone, every pool reaches it — including pools on an
	// Internal switch that issues no DHCP, which are precisely the ones this
	// isolation work targets. Quarantining such a resource over a value that
	// is about to be superseded by its segment address would be the wrong
	// trade, so the read degrades to a warning and no advertised address,
	// the same shape #358 already established for a deferred one.
	ip, err := d.vmIP(ctx, vmName)
	if err != nil {
		slog.Default().WarnContext(ctx, "hyperv: no pre-segment address readable for VM; reporting none (a claimed resource is addressed from its sandbox segment instead)",
			"resource_id", id, "vm_name", vmName, "error", err)
		ip = ""
	}
	timer.step("apply_network")

	// One guest connection under the guest's current (old, pre-rotation)
	// credential. verify_credential inherently needs a *new* connection
	// under the just-rotated credential and is never merged into this one;
	// see its own openGuestSession call below. Before Plan 1c this session
	// was also shared with an apply_network step under the same credential
	// (#361); that step is gone, so rotation is now its only user.
	oldSession, err := d.openGuestSession(ctx, id, guestOS, guestUser, bootstrap.Password, ip)
	if err != nil {
		return nil, fmt.Errorf("open guest session for %s: %w", id, err)
	}
	// The `if oldSession != nil` guard makes this a no-op after the explicit
	// Close + nil-out on the rotate_credential success path below, so it
	// never double-closes.
	defer func() {
		if oldSession != nil {
			oldSession.Close(ctx) //nolint:errcheck,gosec // best-effort fallback close on an early-return path; the success path closes explicitly and checks the error below.
		}
	}()

	rotationResult, err := rotateGuestCredential(ctx, oldSession, guestOS, guestUser, newPassword)
	if err != nil {
		return nil, fmt.Errorf("rotate guest credential for %s: %w", id, err)
	}
	timer.step("rotate_credential")
	if rotationResult == nil || rotationResult.ExitCode != 0 {
		return nil, fmt.Errorf("rotate guest credential for %s failed with exit code %d: %s", id, resultExitCode(rotationResult), resultOutput(rotationResult))
	}

	// Release the session after the checked rotation command completes.
	closeErr := oldSession.Close(ctx)
	oldSession = nil
	timer.step("close_current_credential")
	if closeErr != nil {
		slog.Warn("hyperv: close guest session after rotation", "resource_id", id, "error", closeErr)
	}

	verificationSession, err := d.openGuestSession(ctx, id, guestOS, guestUser, newPassword, ip)
	if err != nil {
		return nil, fmt.Errorf("reconnect with rotated guest credential for %s: %w", id, err)
	}
	defer verificationSession.Close(ctx) //nolint:errcheck,gosec // best-effort close; the verification result itself is what's checked below.
	probeCommand := []string{"whoami"}
	if strings.EqualFold(guestOS, "linux") {
		probeCommand = []string{"id", "-u"}
	}
	verificationResult, err := verificationSession.Exec(ctx, probeCommand[0], probeCommand[1:]...)
	if err != nil {
		return nil, fmt.Errorf("verify rotated guest credential for %s: %w", id, err)
	}
	timer.step("verify_credential")
	if verificationResult == nil || verificationResult.ExitCode != 0 {
		return nil, fmt.Errorf("verify rotated guest credential for %s failed with exit code %d: %s", id, resultExitCode(verificationResult), resultOutput(verificationResult))
	}

	// Recorded only now that the guest has demonstrably accepted the new
	// password, so AttachToSegment can still reach this guest after the
	// control plane drops its copy. See rememberRotatedCredential.
	//
	// Allocation-time only (opts.ApplyNetwork). AttachToSegment is the sole
	// consumer and runs only once a sandbox has claimed this resource; an
	// admission-time preheat rotation has no attach coming, so retaining its
	// password would just leave every unclaimed VM's plaintext credential
	// resident in this process for the pool's whole preheat lifetime.
	if opts.ApplyNetwork {
		d.rememberRotatedCredential(id, guestUser, newPassword)
	}

	credentialData, err := json.Marshal(map[string]string{
		"username": guestUser,
		"password": newPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("encode guest credential for %s: %w", id, err)
	}

	// ip is empty when the VM had no readable address (see the vmIP read
	// above): none is reported here rather than a blank "host"/"ssh_host"
	// (#358 — advertising an address a resource does not have is worse than
	// advertising none). A claimed resource gets its real address from its
	// sandbox's segment at attach time regardless.
	var access map[string]string

	if strings.EqualFold(guestOS, "linux") {
		access = map[string]string{
			"access":   "ssh",
			"ssh_port": "22",
			"ssh_user": guestUser,
		}
		if ip != "" {
			access["ssh_host"] = ip
			access["ssh_cmd"] = fmt.Sprintf("ssh %s@%s", guestUser, ip)
		}
	} else {
		access = map[string]string{
			"access":    "winrm",
			"user":      guestUser,
			"psrp_vmid": id,
		}
		if ip != "" {
			access["host"] = ip
		}
	}

	return &providersdk.GuestPersonalizationResult{
		AccessDetails:       providersdk.GuestAccessDetails{Properties: access},
		EphemeralCredential: &providersdk.GuestCredential{Kind: "password", Data: credentialData},
	}, nil
}

// --- Helpers ---

func (d *Driver) resolveBootstrapCredential(ctx context.Context, id string, notes map[string]string, guestUser string) (providersdk.GuestBootstrapCredential, error) {
	var resolverErr error
	if d.resolveBootstrap != nil {
		bootstrap, err := d.resolveBootstrap(ctx, id)
		if err == nil {
			if strings.TrimSpace(bootstrap.Password) == "" {
				resolverErr = fmt.Errorf("resolver returned an empty password")
			} else {
				if strings.TrimSpace(bootstrap.Username) == "" {
					bootstrap.Username = guestUser
				}
				return bootstrap, nil
			}
		} else {
			resolverErr = err
		}
	}

	if strings.TrimSpace(notes["boxy_guest_password_ref"]) != "" {
		password, err := d.resolveGuestPassword(ctx, notes)
		if err != nil {
			return providersdk.GuestBootstrapCredential{}, err
		}
		return providersdk.GuestBootstrapCredential{Username: guestUser, Password: password}, nil
	}
	if resolverErr != nil {
		return providersdk.GuestBootstrapCredential{}, resolverErr
	}
	return providersdk.GuestBootstrapCredential{}, fmt.Errorf("no control-plane resolver or legacy guest_password_ref is configured")
}

func decodeGuestPassword(credential *providersdk.GuestCredential, defaultUser string) (string, string, error) {
	if credential == nil {
		return "", "", fmt.Errorf("guest credential is required")
	}
	if credential.Kind != "" && credential.Kind != "password" {
		return "", "", fmt.Errorf("unsupported guest credential kind %q", credential.Kind)
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(credential.Data, &payload); err != nil {
		return "", "", fmt.Errorf("decode password payload: %w", err)
	}
	if strings.TrimSpace(payload.Password) == "" {
		return "", "", fmt.Errorf("password payload is empty")
	}
	if strings.TrimSpace(payload.Username) == "" {
		payload.Username = defaultUser
	}
	return payload.Username, payload.Password, nil
}

// guestSession bundles a vmsdk.GuestExec with a Close, satisfying
// vmsdk.GuestSession, so openGuestSession has one return shape regardless of
// whether the underlying transport actually supports holding a connection
// open. Wrapping close in a field (rather than requiring every branch to
// return a *psdirect.Session concretely) lets non-session-capable paths
// (Linux/SSH, and any guestExecFactory test double that doesn't itself
// implement vmsdk.GuestSession) supply a no-op Close instead of forcing
// openGuestSession's callers to type-switch.
type guestSession struct {
	vmsdk.GuestExec
	closeFunc func(ctx context.Context) error
}

func (s *guestSession) Close(ctx context.Context) error {
	if s.closeFunc == nil {
		return nil
	}
	return s.closeFunc(ctx)
}

// openGuestSession returns a vmsdk.GuestSession for one guest-exec
// credential, so a caller with several guest-exec calls to make under that
// same credential (personalizeGuestLocked's apply_network + rotate_credential
// pair, #361) can hold one connection open across them instead of paying a
// fresh PSRP/WinRM session-establishment cost per call.
//
// Windows guests (the only OS personalizeGuestLocked ever runs a
// boxy-managed network-apply step against -- range/static_ip modes are
// rejected for Linux before this is called) use psdirect's native session
// support. Linux/SSH and the guestExecFactory test seam fall back to a
// plain per-call GuestExec wrapped in a no-op Close: SSH has no multi-step
// sequence to merge here (network apply is never boxy-managed on Linux), and
// a test double that wants its own Connect/Close accounting can implement
// vmsdk.GuestSession itself and be used as-is.
func (d *Driver) openGuestSession(ctx context.Context, id, guestOS, guestUser, guestPassword, sshHost string) (vmsdk.GuestSession, error) {
	if d.guestExecFactory != nil {
		exec := d.guestExecFactory(id, guestOS, guestUser, guestPassword, sshHost)
		if session, ok := exec.(vmsdk.GuestSession); ok {
			return session, nil
		}
		return &guestSession{GuestExec: exec}, nil
	}

	if !strings.EqualFold(guestOS, "linux") {
		direct := psdirect.New(id, guestUser, guestPassword)
		session, err := direct.OpenSession(ctx)
		if err != nil {
			return nil, fmt.Errorf("open guest session for %s: %w", id, err)
		}
		return session, nil
	}

	exec, err := d.newGuestExec(ctx, id, guestOS, guestUser, guestPassword, sshHost)
	if err != nil {
		return nil, err
	}
	return &guestSession{GuestExec: exec}, nil
}

func (d *Driver) newGuestExec(ctx context.Context, id, guestOS, guestUser, guestPassword, sshHost string) (vmsdk.GuestExec, error) {
	if d.guestExecFactory != nil {
		if strings.EqualFold(guestOS, "linux") && sshHost == "" {
			vmName, err := d.vmNameFromID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("resolve VM name for %s: %w", id, err)
			}
			sshHost, err = d.vmIP(ctx, vmName)
			if err != nil {
				return nil, fmt.Errorf("get VM IP for %s: %w", vmName, err)
			}
		}
		return d.guestExecFactory(id, guestOS, guestUser, guestPassword, sshHost), nil
	}

	switch strings.ToLower(guestOS) {
	case "linux":
		if sshHost == "" {
			vmName, err := d.vmNameFromID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("resolve VM name for %s: %w", id, err)
			}
			sshHost, err = d.vmIP(ctx, vmName)
			if err != nil {
				return nil, fmt.Errorf("get VM IP for %s: %w", vmName, err)
			}
		}
		return &vmsdk.SSHExec{Host: sshHost, User: guestUser, Password: guestPassword}, nil
	default:
		return psdirect.New(id, guestUser, guestPassword), nil
	}
}

func rotationCommand(guestOS, username, password string) (string, []string) {
	if strings.EqualFold(guestOS, "linux") {
		script := fmt.Sprintf("printf '%%s:%%s\\n' %s %s | chpasswd", shellQuote(username), shellQuote(password))
		return "sh", []string{"-c", script}
	}
	script := fmt.Sprintf("$p=ConvertTo-SecureString '%s' -AsPlainText -Force; Set-LocalUser -Name '%s' -Password $p", psq(password), psq(username))
	return "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", script}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\\"'\\\"'") + "'"
}

func resultExitCode(result *vmsdk.ExecResult) int {
	if result == nil {
		return -1
	}
	return result.ExitCode
}

func resultOutput(result *vmsdk.ExecResult) string {
	if result == nil {
		return "no result"
	}
	return strings.TrimSpace(result.Stderr + " " + result.Stdout)
}

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

// guestIPUnsupportedOnLinux builds the shared "not supported for Linux"
// error, parameterized by which mode (mechanism) was being attempted.
func guestIPUnsupportedOnLinux(mechanism string) error {
	return fmt.Errorf("%s via boxy is not supported for Linux guests; configure the address via cloud-init or a pre-baked image", mechanism)
}

// assignGuestIP configures a static IPv4 address inside the guest using
// PowerShell Direct (VMBus — no guest network required). This is the primary
// mechanism for Windows Server Hyper-V hosts where the virtual switch does not
// issue DHCP leases. Only Windows guests are supported; Linux guests must
// obtain their address via another mechanism (e.g. cloud-init).
//
// Its sole caller is AttachToSegment, which sources ip/prefix/gateway from
// the sandbox's own segment ledger entry (#224, Plan 1c). It was previously
// shared by two pool-declared addressing modes (static_ip and ADR-0012's
// range mode); both were removed when segment addressing made them dead —
// see config.go's note.
//
// The script is idempotent and self-verifying (#235, fixed 2026-08-26):
// a resource's address is normally applied exactly once, but a retry after
// a crash or transient failure (AttachToSegment is contractually
// retryable — see providersdk.NetworkIsolator) still re-applies to an
// already-configured guest, so idempotency remains required, not just
// historically motivated. The original script removed the guest's existing
// IPv4 address but left its default route in place; New-NetIPAddress's own
// -DefaultGateway then rejected the reapply ("Instance DefaultGateway
// already exists") *after* the working address was already torn out,
// leaving the guest on APIPA while the driver still reported success. The
// script now clears the interface's existing default route alongside its
// address before reapplying, and — since no host-side
// Get-VMNetworkAdapter read-back confirms this apply (that view is populated
// by guest integration services and lags a fresh New-NetIPAddress by
// seconds) — re-queries
// the guest's own state immediately after and throws if it doesn't confirm
// the apply: the address must be present in a usable state (Preferred or
// Tentative, not Duplicate/Invalid — a bare presence check would pass on
// exactly the conflict-detection failure this exists to catch), and when a
// gateway was requested, the 0.0.0.0/0 route must exist too. A silent
// in-guest failure of either kind now surfaces as a loud Allocate error
// instead of a healthy-looking but unreachable ready resource.
func (d *Driver) assignGuestIP(ctx context.Context, exec vmsdk.GuestExec, guestOS, ip, prefix, gateway, dns string) error {
	if strings.EqualFold(guestOS, "linux") {
		return guestIPUnsupportedOnLinux("static IP")
	}

	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "24"
	}
	gateway = strings.TrimSpace(gateway)
	dns = strings.TrimSpace(dns)
	if scriptExec, ok := exec.(vmsdk.GuestExecScript); ok {
		result, err := scriptExec.ExecScript(ctx, assignIPScript, ip, prefix, gateway, dns)
		if err != nil {
			return fmt.Errorf("run static IP script: %w", err)
		}
		if result == nil || result.ExitCode != 0 {
			return fmt.Errorf("static IP script exited %d", resultExitCode(result))
		}
		return nil
	}

	// Build the PowerShell script to assign the address inside the guest.
	// We target the first non-disabled adapter ordered by interface index.
	gwBlock := ""
	if gateway != "" {
		gwBlock = fmt.Sprintf(" -DefaultGateway '%s'", psq(gateway))
	}
	dnsBlock := ""
	if dns != "" {
		var quoted []string
		for _, srv := range strings.Split(dns, ",") {
			srv = strings.TrimSpace(srv)
			if srv != "" {
				quoted = append(quoted, fmt.Sprintf("'%s'", psq(srv)))
			}
		}
		if len(quoted) > 0 {
			dnsBlock = fmt.Sprintf(`
Set-DnsClientServerAddress -InterfaceIndex $adapter.InterfaceIndex -ServerAddresses @(%s) | Out-Null`,
				strings.Join(quoted, ", "))
		}
	}

	gwVerifyBlock := ""
	if gateway != "" {
		gwVerifyBlock = fmt.Sprintf(`
$appliedRoute = Get-NetRoute -InterfaceIndex $adapter.InterfaceIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue
if ($null -eq $appliedRoute) { throw "default gateway '%s' did not apply in guest; no 0.0.0.0/0 route found on the interface after New-NetIPAddress" }`, psq(gateway))
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$adapter = Get-NetAdapter | Where-Object { $_.Status -ne 'Disabled' } | Sort-Object InterfaceIndex | Select-Object -First 1
if ($null -eq $adapter) { throw 'no network adapter found in guest' }
$existing = Get-NetIPAddress -InterfaceIndex $adapter.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue
if ($existing) { $existing | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue | Out-Null }
$existingRoute = Get-NetRoute -InterfaceIndex $adapter.InterfaceIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue
if ($existingRoute) { $existingRoute | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue | Out-Null }
New-NetIPAddress -InterfaceIndex $adapter.InterfaceIndex -IPAddress '%s' -PrefixLength %s%s | Out-Null%s
$applied = Get-NetIPAddress -InterfaceIndex $adapter.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -eq '%s' -and $_.AddressState -in @('Preferred', 'Tentative') }
if ($null -eq $applied) { throw "address '%s' did not apply in guest; New-NetIPAddress reported success but the interface shows no usable IPv4 address matching it (duplicate/invalid address state is treated as not applied)" }%s
`, psq(ip), psq(prefix), gwBlock, dnsBlock, psq(ip), psq(ip), gwVerifyBlock)

	result, err := exec.Exec(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return fmt.Errorf("run static IP script: %w", err)
	}
	if result == nil || result.ExitCode != 0 {
		return fmt.Errorf("static IP script exited %d: %s", resultExitCode(result), resultOutput(result))
	}
	return nil
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
