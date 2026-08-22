package devfactory

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

const ProviderType providersdk.Type = "devfactory"

// unlimitedMemoryMB is Availability()'s sentinel for "no configured cap" —
// deliberately not math.MaxInt64. hyperv.Driver.Create converts a MemoryMB
// value to bytes via MemoryMB * 1024 * 1024; MaxInt64 doing that silently
// overflows. This constant is comfortably under math.MaxInt64/(1024*1024)
// (~8.8e12) so the same conversion applied to devfactory's reported value
// can't wrap either, while still reading unambiguously as "far more than
// any real host has," not a plausible real memory figure. See #181.
const unlimitedMemoryMB int64 = 1_000_000_000_000 // 1e12 MB ≈ 1 EB

// simulatedMemoryRequestMB is the fixed RequestedMemoryMB reported by
// FailCreateAs: "capacity" — matches hyperv.Driver's own default VM memory
// request (see hyperv/driver.go's CreateConfig.MemoryMB default) so a
// consumer testing capacity-error handling against devfactory sees
// realistic numbers without needing to configure one.
const simulatedMemoryRequestMB int64 = 2048

// Driver is the devfactory reference implementation of providersdk.Driver.
// State is persisted to a JSON file in DataDir so you can inspect it
// with cat/jq while developing the rest of the system.
type Driver struct {
	cfg     Config
	profile profileSpec
	latency time.Duration
	dataDir string
	mu      sync.Mutex
}

// New creates a devfactory driver from a parsed Config. If DataDir is
// empty, a temporary directory is created.
func New(cfg *Config) *Driver {
	dataDir := cfg.DataDir
	if dataDir == "" {
		dir, err := os.MkdirTemp("", "devfactory-*")
		if err != nil {
			panic(fmt.Sprintf("devfactory: failed to create temp dir: %v", err))
		}
		dataDir = dir
	}

	profile := resolveProfile(cfg.Profile)

	// Use explicit latency if set, otherwise fall back to profile default.
	latency := time.Duration(cfg.Latency)
	if latency == 0 {
		latency = profile.DefaultLatency
	}

	return &Driver{
		cfg:     *cfg,
		profile: profile,
		latency: latency,
		dataDir: dataDir,
	}
}

func (d *Driver) Type() providersdk.Type {
	return ProviderType
}

// Availability implements providersdk.AvailabilityReporter. It returns the
// static value configured via Config.AvailableMemoryMB (or exactly zero if
// AvailableMemoryZero is set) — no per-call memory-request modeling;
// devfactory's Create doesn't decode one today, and adding it purely to
// enforce here would be scope creep unrelated to what this simulator is for
// (letting other code exercise the AvailabilityReporter interface without
// needing a real Hyper-V host). See #181 for AvailableMemoryZero's rationale.
func (d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error) {
	if d.cfg.AvailableMemoryZero {
		return &providersdk.ResourceAvailability{MemoryMB: 0}, nil
	}
	mb := d.cfg.AvailableMemoryMB
	if mb == 0 {
		mb = unlimitedMemoryMB
	}
	return &providersdk.ResourceAvailability{MemoryMB: mb}, nil
}

// List satisfies providersdk.ResourceLister, returning every resource
// tracked in this driver's store — the devfactory analogue of docker's
// managed-label filter and hyperv's boxy-*-name-prefix query: "everything I
// created and haven't deleted," reflected straight from the JSON store
// devfactory already persists to. Sorted by ID since map iteration order
// isn't deterministic.
func (d *Driver) List(ctx context.Context) ([]providersdk.ResourceStatus, error) {
	d.mu.Lock()
	store, err := loadStore(d.dataDir)
	d.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("devfactory: load store: %w", err)
	}

	statuses := make([]providersdk.ResourceStatus, 0, len(store.Resources))
	for _, r := range store.Resources {
		statuses = append(statuses, providersdk.ResourceStatus{ID: r.ID, State: r.State})
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	return statuses, nil
}

// Create provisions a simulated resource. If latency is configured,
// Create blocks for that duration before returning so that the caller
// (e.g. a pool reconciler) experiences realistic provisioning delay.
// This is intentionally synchronous: the pool manager has no async
// polling loop, so the latency must be observed inside Create.
func (d *Driver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	if d.cfg.FailCreate {
		return nil, fmt.Errorf("devfactory: simulated create failure")
	}
	switch d.cfg.FailCreateAs {
	case "capacity":
		return nil, d.simulatedCapacityError(ctx)
	case "orphaned_resource":
		return d.createOrphaned()
	}

	id := generateID()
	now := time.Now()

	d.mu.Lock()
	store, err := loadStore(d.dataDir)
	if err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("devfactory: load store: %w", err)
	}

	port := store.NextPort
	store.NextPort++

	connInfo := d.profile.ConnInfo(port)

	store.Resources[id] = &resourceRecord{
		ID:             id,
		State:          "creating",
		Labels:         d.cfg.Labels,
		ConnectionInfo: connInfo,
		CreatedAt:      now,
	}

	if err := saveStore(d.dataDir, store); err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("devfactory: save store: %w", err)
	}
	d.mu.Unlock()

	// Block for the configured latency, then mark running.
	if d.latency > 0 {
		select {
		case <-ctx.Done():
			// Mirror docker/hyperv's cleanup-on-failed-Create convention
			// (deleteBestEffort): the "creating" record above is otherwise
			// unreachable — Create never returns an ID on this path — and
			// would sit in the store forever, skewing ResourceCount/List.
			d.cleanupAfterCancel(id)
			return nil, ctx.Err()
		case <-time.After(d.latency):
		}
	}

	d.mu.Lock()
	store2, err := loadStore(d.dataDir)
	if err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("devfactory: load store: %w", err)
	}
	if r, ok := store2.Resources[id]; ok {
		r.State = "running"
	}
	if err := saveStore(d.dataDir, store2); err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("devfactory: save store: %w", err)
	}
	d.mu.Unlock()

	return &providersdk.Resource{
		ID:             id,
		ConnectionInfo: connInfo,
		Metadata:       d.cfg.Labels,
	}, nil
}

// simulatedCapacityError builds the *providersdk.CapacityError returned by
// FailCreateAs: "capacity". AvailableMemoryMB is read from the driver's own
// Availability() so combining this with AvailableMemoryZero or a low
// AvailableMemoryMB produces a self-consistent insufficient-capacity error;
// RequestedMemoryMB is fixed (simulatedMemoryRequestMB) rather than derived
// from availability — deriving it (e.g. available+1) would reintroduce the
// same overflow risk unlimitedMemoryMB exists to avoid.
func (d *Driver) simulatedCapacityError(ctx context.Context) error {
	availableMB := int64(0)
	if avail, err := d.Availability(ctx); err == nil && avail != nil {
		availableMB = avail.MemoryMB
	}
	return &providersdk.CapacityError{
		RequestedMemoryMB: simulatedMemoryRequestMB,
		AvailableMemoryMB: availableMB,
	}
}

// createOrphaned implements FailCreateAs: "orphaned_resource". It writes a
// store record in "creating" state — the same shape a normal in-flight
// Create writes before its latency wait — but never advances it to
// "running", simulating a create that partially succeeded and couldn't be
// confirmed torn down. This gives ResourceLister and quarantine/cleanup
// flows something real to find and later Delete, instead of only being
// exercisable against real Hyper-V or a hand-built fake driver.
func (d *Driver) createOrphaned() (*providersdk.Resource, error) {
	id := generateID()

	d.mu.Lock()
	store, err := loadStore(d.dataDir)
	if err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("devfactory: load store: %w", err)
	}
	store.Resources[id] = &resourceRecord{
		ID:        id,
		State:     "creating",
		Labels:    d.cfg.Labels,
		CreatedAt: time.Now(),
	}
	err = saveStore(d.dataDir, store)
	d.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("devfactory: save store: %w", err)
	}

	return nil, &providersdk.OrphanedResourceError{
		ID:           id,
		CauseMessage: "devfactory: simulated create failure (orphaned)",
	}
}

// cleanupAfterCancel best-effort removes a partially-created resource's
// store record when Create's caller cancels ctx mid-provision. Errors are
// swallowed: this is best-effort cleanup on an already-failing path, same
// as docker/hyperv's deleteBestEffort.
func (d *Driver) cleanupAfterCancel(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	store, err := loadStore(d.dataDir)
	if err != nil {
		return
	}
	delete(store.Resources, id)
	_ = saveStore(d.dataDir, store)
}

// Read returns the current state of a simulated resource.
func (d *Driver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	d.mu.Lock()
	store, err := loadStore(d.dataDir)
	d.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("devfactory: load store: %w", err)
	}

	r, ok := store.Resources[id]
	if !ok {
		return nil, fmt.Errorf("devfactory: resource %q not found", id)
	}

	return &providersdk.ResourceStatus{
		ID:    r.ID,
		State: r.State,
	}, nil
}

// Update performs a simulated operation on a resource. Supports ExecOp
// and SetStateOp. All operations are logged in the resource's update
// history.
func (d *Driver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	if d.cfg.FailUpdate {
		return nil, fmt.Errorf("devfactory: simulated update failure")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	store, err := loadStore(d.dataDir)
	if err != nil {
		return nil, fmt.Errorf("devfactory: load store: %w", err)
	}

	r, ok := store.Resources[id]
	if !ok {
		return nil, fmt.Errorf("devfactory: resource %q not found", id)
	}

	desc := fmt.Sprintf("%T", op)
	outputs := map[string]string{"status": "ok"}

	switch o := op.(type) {
	case *ExecOp:
		desc = fmt.Sprintf("exec: %v", o.Command)
		outputs["operation"] = desc
		outputs["stdout"] = fmt.Sprintf("[simulated output of: %v]", o.Command)
		outputs["exit_code"] = "0"
	case *SetStateOp:
		prev := r.State
		desc = fmt.Sprintf("set_state: %s → %s", prev, o.State)
		r.State = o.State
		outputs["operation"] = desc
		outputs["previous_state"] = prev
		outputs["new_state"] = o.State
	default:
		outputs["operation"] = desc
	}

	r.Updates = append(r.Updates, desc)

	if err := saveStore(d.dataDir, store); err != nil {
		return nil, fmt.Errorf("devfactory: save store: %w", err)
	}

	return &providersdk.Result{Outputs: outputs}, nil
}

// UpdateStream emits the simulated command output through the generic event
// sink, allowing end-to-end streaming tests without a real provider.
func (d *Driver) UpdateStream(ctx context.Context, id string, op providersdk.Operation, sink eventstream.Sink) (*providersdk.Result, error) {
	result, err := d.Update(ctx, id, op)
	if err != nil {
		return nil, err
	}
	if output := result.Outputs["stdout"]; output != "" {
		if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stdout"), Payload: []byte(output)}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Delete removes a simulated resource.
func (d *Driver) Delete(ctx context.Context, id string) error {
	if d.cfg.FailDelete {
		return fmt.Errorf("devfactory: simulated delete failure")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	store, err := loadStore(d.dataDir)
	if err != nil {
		return fmt.Errorf("devfactory: load store: %w", err)
	}

	if _, ok := store.Resources[id]; !ok {
		return nil
	}
	delete(store.Resources, id)

	return saveStore(d.dataDir, store)
}

// Allocate performs allocation-time work based on the driver's profile.
// Container: returns a docker exec command using the resource ID.
// VM: generates an RSA SSH keypair to /tmp/boxy/key_<id> and returns SSH info.
// Share: generates random credentials and returns SMB connection info.
func (d *Driver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	d.mu.Lock()
	store, err := loadStore(d.dataDir)
	d.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("devfactory: load store: %w", err)
	}

	r, ok := store.Resources[id]
	if !ok {
		return nil, fmt.Errorf("devfactory: resource %q not found", id)
	}

	switch d.cfg.Profile {
	case ProfileVM:
		host := r.ConnectionInfo["host"]
		keyPath := filepath.Join("/tmp/boxy", "key_"+id)
		if err := generateSSHKey(keyPath); err != nil {
			return nil, fmt.Errorf("devfactory: generate ssh key: %w", err)
		}
		return map[string]any{
			"access":   "ssh",
			"ssh_user": "admin",
			"ssh_key":  keyPath,
			"ssh_cmd":  fmt.Sprintf("ssh -i %s admin@%s", keyPath, host),
		}, nil

	case ProfileShare:
		pass, err := generatePassword()
		if err != nil {
			return nil, fmt.Errorf("devfactory: generate password: %w", err)
		}
		return map[string]any{
			"access":     "smb",
			"username":   "boxy-test-user",
			"password":   pass,
			"unc_path":   r.ConnectionInfo["unc_path"],
			"mount_path": r.ConnectionInfo["mount_path"],
		}, nil

	default: // ProfileContainer
		return map[string]any{
			"access": "docker-exec",
			"exec":   fmt.Sprintf("docker exec -it %s /bin/sh", id),
		}, nil
	}
}

func generateSSHKey(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	// os.WriteFile's mode argument is only applied by the OS on file
	// creation, not on rewrite of a pre-existing file — see #158.
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func generatePassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- Test helpers ---

// DataDir returns the directory where the store file lives.
func (d *Driver) DataDir() string {
	return d.dataDir
}

// ResourceCount returns the number of tracked resources.
func (d *Driver) ResourceCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	store, err := loadStore(d.dataDir)
	if err != nil {
		return 0
	}
	return len(store.Resources)
}

// ResourceUpdates returns the update log for a resource.
func (d *Driver) ResourceUpdates(id string) ([]string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	store, err := loadStore(d.dataDir)
	if err != nil {
		return nil, false
	}
	r, ok := store.Resources[id]
	if !ok {
		return nil, false
	}
	out := make([]string, len(r.Updates))
	copy(out, r.Updates)
	return out, true
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "dev-" + hex.EncodeToString(b)
}
