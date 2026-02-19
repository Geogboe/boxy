package devbox

import (
	"context"
	"testing"
	"time"
)

func TestDriver_Create(t *testing.T) {
	d := New(&Config{})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ID == "" {
		t.Fatal("expected non-empty resource ID")
	}
	if res.ConnectionInfo["type"] != "devbox" {
		t.Errorf("expected connection type devbox, got %q", res.ConnectionInfo["type"])
	}
	if d.ResourceCount() != 1 {
		t.Errorf("expected 1 resource, got %d", d.ResourceCount())
	}
}

func TestDriver_Create_WithLatency(t *testing.T) {
	d := New(&Config{Latency: 50 * time.Millisecond})

	start := time.Now()
	_, err := d.Create(context.Background(), nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected >= 50ms latency, got %v", elapsed)
	}
}

func TestDriver_Create_ContextCancelled(t *testing.T) {
	d := New(&Config{Latency: 5 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := d.Create(ctx, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDriver_Create_Failure(t *testing.T) {
	d := New(&Config{FailCreate: true})

	_, err := d.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("expected create failure")
	}
}

func TestDriver_Read(t *testing.T) {
	d := New(&Config{})

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
	d := New(&Config{})

	_, err := d.Read(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent resource")
	}
}

func TestDriver_Update_Exec(t *testing.T) {
	d := New(&Config{})

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

	updates, ok := d.ResourceUpdates(res.ID)
	if !ok {
		t.Fatal("resource not found after update")
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
}

func TestDriver_Update_Failure(t *testing.T) {
	d := New(&Config{FailUpdate: true})

	res, _ := d.Create(context.Background(), nil)
	_, err := d.Update(context.Background(), res.ID, &ExecOp{
		Command: []string{"echo", "hello"},
	})
	if err == nil {
		t.Fatal("expected update failure")
	}
}

func TestDriver_Delete(t *testing.T) {
	d := New(&Config{})

	res, _ := d.Create(context.Background(), nil)
	if err := d.Delete(context.Background(), res.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if d.ResourceCount() != 0 {
		t.Errorf("expected 0 resources after delete, got %d", d.ResourceCount())
	}
}

func TestDriver_Delete_NotFound(t *testing.T) {
	d := New(&Config{})

	err := d.Delete(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent resource")
	}
}

func TestDriver_Delete_Failure(t *testing.T) {
	d := New(&Config{FailDelete: true})

	res, _ := d.Create(context.Background(), nil)
	err := d.Delete(context.Background(), res.ID)
	if err == nil {
		t.Fatal("expected delete failure")
	}
}

func TestDriver_Labels(t *testing.T) {
	d := New(&Config{
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
