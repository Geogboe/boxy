package devfactory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestDriver(t *testing.T, cfg *Config) *Driver {
	t.Helper()
	cfg.DataDir = t.TempDir()
	return New(cfg)
}

func TestDriver_Create(t *testing.T) {
	d := newTestDriver(t, &Config{})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ID == "" {
		t.Fatal("expected non-empty resource ID")
	}
	if res.ConnectionInfo["type"] != "container" {
		t.Errorf("expected connection type container (default profile), got %q", res.ConnectionInfo["type"])
	}
	if d.ResourceCount() != 1 {
		t.Errorf("expected 1 resource, got %d", d.ResourceCount())
	}
}

func TestDriver_Create_UniqueConnectionInfo(t *testing.T) {
	d := newTestDriver(t, &Config{})

	r1, _ := d.Create(context.Background(), nil)
	r2, _ := d.Create(context.Background(), nil)

	if r1.ConnectionInfo["port"] == r2.ConnectionInfo["port"] {
		t.Errorf("expected unique ports, both got %q", r1.ConnectionInfo["port"])
	}
}

func TestDriver_Create_StateTransition(t *testing.T) {
	d := newTestDriver(t, &Config{Latency: 50 * time.Millisecond})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should start in "creating" state.
	status, err := d.Read(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if status.State != "creating" {
		t.Errorf("expected initial state creating, got %q", status.State)
	}

	// Wait for transition.
	time.Sleep(100 * time.Millisecond)

	status, err = d.Read(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("Read after transition: %v", err)
	}
	if status.State != "running" {
		t.Errorf("expected state running after latency, got %q", status.State)
	}
}

func TestDriver_Create_ZeroLatency(t *testing.T) {
	d := newTestDriver(t, &Config{})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	status, _ := d.Read(context.Background(), res.ID)
	if status.State != "running" {
		t.Errorf("expected immediate running state, got %q", status.State)
	}
}

func TestDriver_Create_Failure(t *testing.T) {
	d := newTestDriver(t, &Config{FailCreate: true})

	_, err := d.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("expected create failure")
	}
}

func TestDriver_Read(t *testing.T) {
	d := newTestDriver(t, &Config{})

	res, _ := d.Create(context.Background(), nil)
	status, err := d.Read(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if status.State != "running" {
		t.Errorf("expected state running, got %q", status.State)
	}
}

func TestDriver_Read_NotFound(t *testing.T) {
	d := newTestDriver(t, &Config{})

	_, err := d.Read(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent resource")
	}
}

func TestDriver_Update_Exec(t *testing.T) {
	d := newTestDriver(t, &Config{})

	res, _ := d.Create(context.Background(), nil)
	result, err := d.Update(context.Background(), res.ID, &ExecOp{
		Command: []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Outputs["status"] != "ok" {
		t.Errorf("expected status ok, got %q", result.Outputs["status"])
	}
	if result.Outputs["stdout"] == "" {
		t.Error("expected simulated stdout output")
	}

	updates, ok := d.ResourceUpdates(res.ID)
	if !ok {
		t.Fatal("resource not found after update")
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
}

func TestDriver_Update_SetState(t *testing.T) {
	d := newTestDriver(t, &Config{})

	res, _ := d.Create(context.Background(), nil)

	_, err := d.Update(context.Background(), res.ID, &SetStateOp{State: "stopped"})
	if err != nil {
		t.Fatalf("Update SetState: %v", err)
	}

	status, _ := d.Read(context.Background(), res.ID)
	if status.State != "stopped" {
		t.Errorf("expected state stopped, got %q", status.State)
	}
}

func TestDriver_Update_Failure(t *testing.T) {
	d := newTestDriver(t, &Config{FailUpdate: true})

	res, _ := d.Create(context.Background(), nil)
	_, err := d.Update(context.Background(), res.ID, &ExecOp{
		Command: []string{"echo", "hello"},
	})
	if err == nil {
		t.Fatal("expected update failure")
	}
}

func TestDriver_Delete(t *testing.T) {
	d := newTestDriver(t, &Config{})

	res, _ := d.Create(context.Background(), nil)
	if err := d.Delete(context.Background(), res.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if d.ResourceCount() != 0 {
		t.Errorf("expected 0 resources after delete, got %d", d.ResourceCount())
	}
}

func TestDriver_Delete_NotFound(t *testing.T) {
	d := newTestDriver(t, &Config{})

	err := d.Delete(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent resource")
	}
}

func TestDriver_Delete_Failure(t *testing.T) {
	d := newTestDriver(t, &Config{FailDelete: true})

	res, _ := d.Create(context.Background(), nil)
	err := d.Delete(context.Background(), res.ID)
	if err == nil {
		t.Fatal("expected delete failure")
	}
}

func TestDriver_Labels(t *testing.T) {
	d := newTestDriver(t, &Config{
		Labels: map[string]string{"env": "test", "role": "attacker"},
	})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Metadata["role"] != "attacker" {
		t.Errorf("expected role=attacker in metadata, got %q", res.Metadata["role"])
	}
}

func TestDriver_Persistence(t *testing.T) {
	dataDir := t.TempDir()

	// Create a resource with driver 1.
	d1 := New(&Config{DataDir: dataDir})
	res, err := d1.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create a new driver pointing at the same directory.
	d2 := New(&Config{DataDir: dataDir})
	if d2.ResourceCount() != 1 {
		t.Fatalf("expected 1 resource from persisted store, got %d", d2.ResourceCount())
	}

	status, err := d2.Read(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("Read from new driver: %v", err)
	}
	if status.State != "running" {
		t.Errorf("expected state running, got %q", status.State)
	}
}

func TestDriver_JSONFileReadable(t *testing.T) {
	d := newTestDriver(t, &Config{
		Labels: map[string]string{"env": "test"},
	})

	res, _ := d.Create(context.Background(), nil)
	d.Update(context.Background(), res.ID, &ExecOp{Command: []string{"whoami"}})

	// Read and parse the JSON file directly.
	data, err := os.ReadFile(filepath.Join(d.DataDir(), storeFilename))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var store storeData
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(store.Resources) != 1 {
		t.Fatalf("expected 1 resource in JSON, got %d", len(store.Resources))
	}

	r := store.Resources[res.ID]
	if r.State != "running" {
		t.Errorf("expected state running in JSON, got %q", r.State)
	}
	if len(r.Updates) != 1 {
		t.Errorf("expected 1 update in JSON, got %d", len(r.Updates))
	}
	if r.ConnectionInfo["type"] != "container" {
		t.Errorf("expected connection type container in JSON, got %q", r.ConnectionInfo["type"])
	}
}

func TestDriver_Profile_Container(t *testing.T) {
	d := newTestDriver(t, &Config{Profile: ProfileContainer})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ConnectionInfo["type"] != "container" {
		t.Errorf("expected type container, got %q", res.ConnectionInfo["type"])
	}
	if res.ConnectionInfo["host"] == "" || res.ConnectionInfo["port"] == "" {
		t.Error("expected host and port in container connection info")
	}
}

func TestDriver_Profile_VM(t *testing.T) {
	// Override default latency to 0 so test is fast.
	d := newTestDriver(t, &Config{Profile: ProfileVM, Latency: 1 * time.Millisecond})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ConnectionInfo["type"] != "vm" {
		t.Errorf("expected type vm, got %q", res.ConnectionInfo["type"])
	}
	if res.ConnectionInfo["ssh_port"] != "22" {
		t.Errorf("expected ssh_port 22, got %q", res.ConnectionInfo["ssh_port"])
	}
	if res.ConnectionInfo["ssh_user"] != "admin" {
		t.Errorf("expected ssh_user admin, got %q", res.ConnectionInfo["ssh_user"])
	}
	if res.ConnectionInfo["ssh_key"] == "" {
		t.Error("expected ssh_key in VM connection info")
	}
}

func TestDriver_Profile_Share(t *testing.T) {
	d := newTestDriver(t, &Config{Profile: ProfileShare})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ConnectionInfo["type"] != "share" {
		t.Errorf("expected type share, got %q", res.ConnectionInfo["type"])
	}
	if res.ConnectionInfo["unc_path"] == "" {
		t.Error("expected unc_path in share connection info")
	}
	if res.ConnectionInfo["mount_path"] == "" {
		t.Error("expected mount_path in share connection info")
	}
}

func TestDriver_Profile_VMDefaultLatency(t *testing.T) {
	// VM profile has a 2s default latency. Resource should start as "creating".
	d := newTestDriver(t, &Config{Profile: ProfileVM})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	status, _ := d.Read(context.Background(), res.ID)
	if status.State != "creating" {
		t.Errorf("expected VM to start in creating state (default latency), got %q", status.State)
	}
}

func TestRegistration(t *testing.T) {
	reg := Registration()

	if reg.Type != ProviderType {
		t.Errorf("expected type %q, got %q", ProviderType, reg.Type)
	}

	cfg := reg.ConfigProto()
	if _, ok := cfg.(*Config); !ok {
		t.Fatalf("expected *Config, got %T", cfg)
	}

	driver, err := reg.NewDriver(cfg)
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	if driver.Type() != ProviderType {
		t.Errorf("expected driver type %q, got %q", ProviderType, driver.Type())
	}
}
