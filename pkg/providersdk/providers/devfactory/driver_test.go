package devfactory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
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
	// Create now blocks for the configured latency and returns with the
	// resource already in "running" state — no intermediate polling needed.
	d := newTestDriver(t, &Config{Latency: Duration(50 * time.Millisecond)})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	status, err := d.Read(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if status.State != "running" {
		t.Errorf("expected running after Create returned, got %q", status.State)
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
		Env:     map[string]string{"Foo": "bar", "SECRET": "never persist"},
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
	execs, ok := d.ResourceExecs(res.ID)
	if !ok || len(execs) != 1 {
		t.Fatalf("exec records = %#v, found = %t", execs, ok)
	}
	if got, want := execs[0].EnvironmentKeys, []string{"Foo", "SECRET"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("environment keys = %#v, want %#v", got, want)
	}
	encoded, err := json.Marshal(execs[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "never persist") {
		t.Fatal("exec record persisted an environment value")
	}
}

func TestDriver_Update_ScriptUsesDeterministicGuestCache(t *testing.T) {
	d := newTestDriver(t, &Config{Profile: ProfileContainer})
	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	script, err := providersdk.NewScriptSpec([]byte("echo script\n"), providersdk.ScriptInterpreterAuto, []string{"quoted value", "--mode", "ci"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.Update(context.Background(), res.ID, &ExecOp{Script: script})
	if err != nil {
		t.Fatalf("first script update: %v", err)
	}
	second, err := d.Update(context.Background(), res.ID, &ExecOp{Script: script})
	if err != nil {
		t.Fatalf("second script update: %v", err)
	}
	if first.Outputs["script_cache"] != "miss" || second.Outputs["script_cache"] != "hit" {
		t.Fatalf("cache outputs = %q, %q, want miss then hit", first.Outputs["script_cache"], second.Outputs["script_cache"])
	}
	if first.Outputs["script_digest"] != script.Digest || first.Outputs["script_interpreter"] != "sh" {
		t.Fatalf("script metadata = %#v", first.Outputs)
	}
	execs, ok := d.ResourceExecs(res.ID)
	if !ok || len(execs) != 2 {
		t.Fatalf("exec records = %#v", execs)
	}
	encoded, err := json.Marshal(execs[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "echo script") {
		t.Fatal("script content was persisted in the execution record")
	}
	if !reflect.DeepEqual(execs[0].ScriptArgs, []string{"quoted value", "--mode", "ci"}) {
		t.Fatalf("script args = %#v", execs[0].ScriptArgs)
	}
}

type devfactoryEventSink struct {
	events []eventstream.Event
}

func (s *devfactoryEventSink) Send(_ context.Context, event eventstream.Event) error {
	s.events = append(s.events, event)
	return nil
}

func TestDriver_UpdateStream_EmitsConfiguredChunks(t *testing.T) {
	d := newTestDriver(t, &Config{
		ExecOutputChunks: []string{"first\n", "second\n"},
		ExecChunkDelay:   Duration(time.Millisecond),
	})
	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sink := new(devfactoryEventSink)
	result, err := d.UpdateStream(context.Background(), res.ID, &ExecOp{Command: []string{"long-running"}}, sink)
	if err != nil {
		t.Fatalf("UpdateStream: %v", err)
	}
	if result.Outputs["stdout"] != "first\nsecond\n" {
		t.Fatalf("joined stdout = %q, want configured chunks joined", result.Outputs["stdout"])
	}
	if len(sink.events) != 2 {
		t.Fatalf("events = %#v, want two output events", sink.events)
	}
	if got := string(sink.events[0].Payload); got != "first\n" {
		t.Fatalf("first chunk = %q, want first newline", got)
	}
	if got := string(sink.events[1].Payload); got != "second\n" {
		t.Fatalf("second chunk = %q, want second newline", got)
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
	if err != nil {
		t.Fatalf("Delete missing resource: %v", err)
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
	_, _ = d.Update(context.Background(), res.ID, &ExecOp{Command: []string{"whoami"}})

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
	d := newTestDriver(t, &Config{Profile: ProfileVM, Latency: Duration(1 * time.Millisecond)})

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
	// VM profile has a 2s default latency. Use a short explicit latency
	// to keep tests fast; the blocking behaviour is already covered by
	// TestDriver_Create_StateTransition.
	d := newTestDriver(t, &Config{Profile: ProfileVM, Latency: Duration(1 * time.Millisecond)})

	res, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	status, _ := d.Read(context.Background(), res.ID)
	if status.State != "running" {
		t.Errorf("expected running after Create returned, got %q", status.State)
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

func TestDriver_Availability_ReturnsConfiguredValue(t *testing.T) {
	d := New(&Config{AvailableMemoryMB: 4096, DataDir: t.TempDir()})

	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avail.MemoryMB != 4096 {
		t.Errorf("MemoryMB = %d, want 4096", avail.MemoryMB)
	}
}

func TestDriver_Availability_ZeroMeansUnlimited(t *testing.T) {
	d := New(&Config{DataDir: t.TempDir()})

	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avail.MemoryMB != UnlimitedMemoryMB {
		t.Errorf("MemoryMB = %d, want UnlimitedMemoryMB (%d)", avail.MemoryMB, UnlimitedMemoryMB)
	}
	// The sentinel must not overflow the MB->bytes conversion real drivers
	// (e.g. hyperv.Driver.Create) perform on a MemoryMB value. See #181.
	if bytes := avail.MemoryMB * 1024 * 1024; bytes <= 0 {
		t.Errorf("UnlimitedMemoryMB * 1024 * 1024 overflowed int64: got %d", bytes)
	}
}

func TestDriver_Availability_ZeroFlag_ReportsRealZero(t *testing.T) {
	d := New(&Config{AvailableMemoryZero: true, AvailableMemoryMB: 4096, DataDir: t.TempDir()})

	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avail.MemoryMB != 0 {
		t.Errorf("MemoryMB = %d, want 0 (AvailableMemoryZero overrides AvailableMemoryMB)", avail.MemoryMB)
	}
}

func TestDriver_Availability_PositiveValueUnaffectedByFix(t *testing.T) {
	d := New(&Config{AvailableMemoryMB: 4096, DataDir: t.TempDir()})

	avail, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avail.MemoryMB != 4096 {
		t.Errorf("MemoryMB = %d, want 4096", avail.MemoryMB)
	}
}

// --- ResourceLister ---

func TestDriver_List_Empty(t *testing.T) {
	d := newTestDriver(t, &Config{})

	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected 0 resources, got %d", len(statuses))
	}
}

func TestDriver_List_ReturnsTrackedResources(t *testing.T) {
	d := newTestDriver(t, &Config{})

	r1, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r2, err := d.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(statuses))
	}

	ids := map[string]string{statuses[0].ID: statuses[0].State, statuses[1].ID: statuses[1].State}
	for _, id := range []string{r1.ID, r2.ID} {
		state, ok := ids[id]
		if !ok {
			t.Errorf("expected resource %q in List() result", id)
			continue
		}
		if state != "running" {
			t.Errorf("resource %q state = %q, want running", id, state)
		}
	}

	if !sort.SliceIsSorted(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID }) {
		t.Error("expected List() results sorted by ID for deterministic output")
	}
}

func TestDriver_List_FailListSimulatesUnverifiableInventory(t *testing.T) {
	d := newTestDriver(t, &Config{FailList: true})
	if _, err := d.List(context.Background()); err == nil {
		t.Fatal("List error = nil, want simulated incomplete-listing error")
	}
}

// --- FailCreateAs: typed-error simulation ---

func TestDriver_Create_FailCreateAsCapacity_AvailableMemoryZeroConfigured(t *testing.T) {
	d := newTestDriver(t, &Config{FailCreateAs: "capacity", AvailableMemoryZero: true})

	_, err := d.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var capErr *providersdk.CapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected *providersdk.CapacityError, got %#v", err)
	}
	if capErr.RequestedMemoryMB != simulatedMemoryRequestMB {
		t.Errorf("RequestedMemoryMB = %d, want %d", capErr.RequestedMemoryMB, simulatedMemoryRequestMB)
	}
	if capErr.AvailableMemoryMB != 0 {
		t.Errorf("AvailableMemoryMB = %d, want 0 (AvailableMemoryZero configured)", capErr.AvailableMemoryMB)
	}
	if d.ResourceCount() != 0 {
		t.Errorf("expected no resource written for a capacity failure, got %d", d.ResourceCount())
	}
}

// TestDriver_Create_FailCreateAsCapacity_DefaultIsCoherent verifies
// FailCreateAs: "capacity" produces a self-consistent error even with no
// other config set. Without the clamp in simulatedCapacityError, this would
// report AvailableMemoryMB == UnlimitedMemoryMB alongside a much smaller
// RequestedMemoryMB — a nonsensical "capacity failure" a consumer would
// hit by default just from setting this one knob.
func TestDriver_Create_FailCreateAsCapacity_DefaultIsCoherent(t *testing.T) {
	d := newTestDriver(t, &Config{FailCreateAs: "capacity"})

	_, err := d.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var capErr *providersdk.CapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected *providersdk.CapacityError, got %#v", err)
	}
	if capErr.AvailableMemoryMB >= capErr.RequestedMemoryMB {
		t.Errorf("AvailableMemoryMB (%d) >= RequestedMemoryMB (%d): not a coherent capacity failure",
			capErr.AvailableMemoryMB, capErr.RequestedMemoryMB)
	}
}

func TestDriver_Create_FailCreateAsOrphanedResource(t *testing.T) {
	d := newTestDriver(t, &Config{FailCreateAs: "orphaned_resource"})

	_, err := d.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var orphanErr *providersdk.OrphanedResourceError
	if !errors.As(err, &orphanErr) {
		t.Fatalf("expected *providersdk.OrphanedResourceError, got %#v", err)
	}
	if orphanErr.ID == "" {
		t.Fatal("expected non-empty orphaned resource ID")
	}

	// The orphaned record must be discoverable via List()...
	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, s := range statuses {
		if s.ID == orphanErr.ID {
			found = true
			if s.State != "creating" {
				t.Errorf("orphaned resource state = %q, want creating", s.State)
			}
		}
	}
	if !found {
		t.Fatalf("expected orphaned resource %q in List() result", orphanErr.ID)
	}

	// ...and cleanable via Delete(), completing the quarantine round trip.
	if err := d.Delete(context.Background(), orphanErr.ID); err != nil {
		t.Fatalf("Delete orphaned resource: %v", err)
	}
	if d.ResourceCount() != 0 {
		t.Errorf("expected orphaned resource removed after Delete, got %d resources", d.ResourceCount())
	}
}

func TestDriver_Create_FailCreateTakesPrecedenceOverFailCreateAs(t *testing.T) {
	d := newTestDriver(t, &Config{FailCreate: true, FailCreateAs: "capacity"})

	_, err := d.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var capErr *providersdk.CapacityError
	if errors.As(err, &capErr) {
		t.Fatal("expected plain FailCreate error, not *providersdk.CapacityError")
	}
}

// --- ctx-cancellation cleanup ---

func TestDriver_Create_CtxCancelDuringLatency_CleansUp(t *testing.T) {
	d := newTestDriver(t, &Config{Latency: Duration(200 * time.Millisecond)})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := d.Create(ctx, nil)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}

	if d.ResourceCount() != 0 {
		t.Errorf("expected the partial resource cleaned up after ctx cancellation, got %d", d.ResourceCount())
	}
	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected no resources in List() after ctx cancellation, got %d", len(statuses))
	}
}

// --- providersdk interface compliance ---

var _ providersdk.AvailabilityReporter = (*Driver)(nil)
var _ providersdk.ResourceLister = (*Driver)(nil)
var _ providersdk.RelativePathResolver = (*Config)(nil)
