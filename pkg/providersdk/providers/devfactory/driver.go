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
	"sync"
	"time"

	"github.com/Geogboe/boxy/pkg/providersdk"
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

// Create provisions a simulated resource. If latency is configured,
// Create blocks for that duration before returning so that the caller
// (e.g. a pool reconciler) experiences realistic provisioning delay.
// This is intentionally synchronous: the pool manager has no async
// polling loop, so the latency must be observed inside Create.
func (d *Driver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	if d.cfg.FailCreate {
		return nil, fmt.Errorf("devfactory: simulated create failure")
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
			"username":   "svc_boxy",
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
	return os.WriteFile(path, pem.EncodeToMemory(block), 0600)
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
