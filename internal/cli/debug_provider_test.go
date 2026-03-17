package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugProvider_lifecycle(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	// 1. Create a resource — capture ID from the devfactory store file.
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"debug", "provider", "create", "--data-dir", dataDir})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Read the store file to find the created resource ID.
	id := firstResourceID(t, dataDir)

	// 2. List resources.
	cmd = NewRootCommand()
	cmd.SetArgs([]string{"debug", "provider", "list", "--data-dir", dataDir})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list: %v", err)
	}

	// 3. Get the created resource.
	cmd = NewRootCommand()
	cmd.SetArgs([]string{"debug", "provider", "get", id, "--data-dir", dataDir})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get: %v", err)
	}

	// 4. Exec a command on it.
	cmd = NewRootCommand()
	cmd.SetArgs([]string{"debug", "provider", "--data-dir", dataDir, "exec", id, "--", "echo", "hello"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}

	// 5. Set state.
	cmd = NewRootCommand()
	cmd.SetArgs([]string{"debug", "provider", "set-state", id, "stopped", "--data-dir", dataDir})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("set-state: %v", err)
	}

	// 6. Delete the resource.
	cmd = NewRootCommand()
	cmd.SetArgs([]string{"debug", "provider", "delete", id, "--data-dir", dataDir})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// 7. Verify get fails after delete.
	cmd = NewRootCommand()
	cmd.SetArgs([]string{"debug", "provider", "get", id, "--data-dir", dataDir})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error getting deleted resource")
	}
}

// firstResourceID reads the devfactory store and returns the first resource ID.
func firstResourceID(t *testing.T, dataDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dataDir, "devfactory.json"))
	if err != nil {
		t.Fatalf("reading devfactory store: %v", err)
	}
	var store struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("parsing devfactory store: %v", err)
	}
	for id := range store.Resources {
		return id
	}
	t.Fatal("no resources in store after create")
	return ""
}
