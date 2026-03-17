package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// setupSandboxStore creates a DiskStore in a temp dir with optional seed sandboxes.
func setupSandboxStore(t *testing.T, sandboxes ...model.Sandbox) (storePath string) {
	t.Helper()
	dir := t.TempDir()
	storePath = filepath.Join(dir, "state.json")

	st, err := store.NewDiskStore(storePath)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ctx := context.Background()
	for _, sb := range sandboxes {
		if err := st.CreateSandbox(ctx, sb); err != nil {
			t.Fatalf("CreateSandbox(%s): %v", sb.ID, err)
		}
	}
	return storePath
}

func TestSandboxList_empty(t *testing.T) {
	t.Parallel()
	storePath := setupSandboxStore(t)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "list", "--state", storePath})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestSandboxList_with_sandboxes(t *testing.T) {
	t.Parallel()
	storePath := setupSandboxStore(t,
		model.Sandbox{ID: "sb-1", Name: "one"},
		model.Sandbox{ID: "sb-2", Name: "two"},
	)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "list", "--state", storePath})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestSandboxGet_found(t *testing.T) {
	t.Parallel()
	storePath := setupSandboxStore(t,
		model.Sandbox{ID: "sb-1", Name: "test-sandbox"},
	)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "get", "sb-1", "--state", storePath})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestSandboxGet_not_found(t *testing.T) {
	t.Parallel()
	storePath := setupSandboxStore(t)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "get", "no-such-id", "--state", storePath})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for non-existent sandbox")
	}
}

func TestSandboxDelete_found(t *testing.T) {
	t.Parallel()
	storePath := setupSandboxStore(t,
		model.Sandbox{ID: "sb-1", Name: "to-delete"},
	)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "delete", "sb-1", "--state", storePath})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Verify it's gone.
	st, err := store.NewDiskStore(storePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	_, err = st.GetSandbox(context.Background(), "sb-1")
	if err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestSandboxDelete_not_found(t *testing.T) {
	t.Parallel()
	storePath := setupSandboxStore(t)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "delete", "no-such-id", "--state", storePath})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for non-existent sandbox")
	}
}
