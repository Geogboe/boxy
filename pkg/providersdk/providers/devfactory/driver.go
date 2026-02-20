package devfactory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

const ProviderType providersdk.Type = "devfactory"

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
	latency := cfg.Latency
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

// Create provisions a simulated resource. The resource starts in
// "creating" state and transitions to "running" after the configured
// latency (in a background goroutine). If latency is zero the resource
// is immediately "running".
func (d *Driver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	if d.cfg.FailCreate {
		return nil, fmt.Errorf("devfactory: simulated create failure")
	}

	id := generateID()
	now := time.Now()

	initialState := "running"
	if d.latency > 0 {
		initialState = "creating"
	}

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
		State:          initialState,
		Labels:         d.cfg.Labels,
		ConnectionInfo: connInfo,
		CreatedAt:      now,
	}

	if err := saveStore(d.dataDir, store); err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("devfactory: save store: %w", err)
	}
	d.mu.Unlock()

	// Transition creating → running in the background.
	if d.latency > 0 {
		go d.transitionAfter(id, d.latency)
	}

	return &providersdk.Resource{
		ID:             id,
		ConnectionInfo: connInfo,
		Metadata:       d.cfg.Labels,
	}, nil
}

// transitionAfter waits for the given duration then sets the resource
// state to "running". If the resource has been deleted in the meantime,
// this is a no-op.
func (d *Driver) transitionAfter(id string, delay time.Duration) {
	time.Sleep(delay)

	d.mu.Lock()
	defer d.mu.Unlock()

	store, err := loadStore(d.dataDir)
	if err != nil {
		return
	}
	r, ok := store.Resources[id]
	if !ok || r.State != "creating" {
		return
	}
	r.State = "running"
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
		return fmt.Errorf("devfactory: resource %q not found", id)
	}
	delete(store.Resources, id)

	return saveStore(d.dataDir, store)
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
