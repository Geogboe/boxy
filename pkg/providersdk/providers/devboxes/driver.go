package devboxes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

const ProviderType providersdk.Type = "devboxes"

// resource is an in-memory simulated resource.
type resource struct {
	id        string
	state     string
	labels    map[string]string
	createdAt time.Time
	updates   []string // log of operations applied
}

// Driver is the devboxes reference implementation of providersdk.Driver.
// All resources live in memory and cost nothing to create or destroy.
type Driver struct {
	cfg       Config
	mu        sync.Mutex
	resources map[string]*resource
}

// New creates a devboxes driver from a parsed Config.
func New(cfg *Config) *Driver {
	return &Driver{
		cfg:       *cfg,
		resources: make(map[string]*resource),
	}
}

func (d *Driver) Type() providersdk.Type {
	return ProviderType
}

// Create provisions a simulated resource. Returns immediately (or after
// configured latency) with a resource that has fake connection info.
func (d *Driver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	if d.cfg.FailCreate {
		return nil, fmt.Errorf("devboxes: simulated create failure")
	}

	// Respect configured latency.
	if d.cfg.Latency > 0 {
		select {
		case <-time.After(d.cfg.Latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	id := generateID()

	d.mu.Lock()
	d.resources[id] = &resource{
		id:        id,
		state:     "running",
		labels:    d.cfg.Labels,
		createdAt: time.Now(),
	}
	d.mu.Unlock()

	return &providersdk.Resource{
		ID: id,
		ConnectionInfo: map[string]string{
			"host": "127.0.0.1",
			"port": "22",
			"type": "devboxes",
		},
		Metadata: d.cfg.Labels,
	}, nil
}

// Read returns the current state of a simulated resource.
func (d *Driver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	d.mu.Lock()
	r, ok := d.resources[id]
	d.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("devboxes: resource %q not found", id)
	}

	return &providersdk.ResourceStatus{
		ID:    r.id,
		State: r.state,
	}, nil
}

// Update performs a simulated operation on a resource. It logs the
// operation and returns a result. The concrete Operation types are
// defined in operations.go.
func (d *Driver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	if d.cfg.FailUpdate {
		return nil, fmt.Errorf("devboxes: simulated update failure")
	}

	d.mu.Lock()
	r, ok := d.resources[id]
	if !ok {
		d.mu.Unlock()
		return nil, fmt.Errorf("devboxes: resource %q not found", id)
	}

	desc := fmt.Sprintf("%T", op)
	if e, ok := op.(*ExecOp); ok {
		desc = fmt.Sprintf("exec: %v", e.Command)
	}
	r.updates = append(r.updates, desc)
	d.mu.Unlock()

	return &providersdk.Result{
		Outputs: map[string]string{
			"status":    "ok",
			"operation": desc,
		},
	}, nil
}

// Delete removes a simulated resource from memory.
func (d *Driver) Delete(ctx context.Context, id string) error {
	if d.cfg.FailDelete {
		return fmt.Errorf("devboxes: simulated delete failure")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.resources[id]; !ok {
		return fmt.Errorf("devboxes: resource %q not found", id)
	}
	delete(d.resources, id)
	return nil
}

// ResourceCount returns the number of tracked resources. Useful in tests.
func (d *Driver) ResourceCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.resources)
}

// ResourceUpdates returns the update log for a resource. Useful in tests.
func (d *Driver) ResourceUpdates(id string) ([]string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.resources[id]
	if !ok {
		return nil, false
	}
	out := make([]string, len(r.updates))
	copy(out, r.updates)
	return out, true
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "devbox-" + hex.EncodeToString(b)
}
