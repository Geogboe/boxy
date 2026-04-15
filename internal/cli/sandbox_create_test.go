package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestFailLocalSandboxCreate_MarksSandboxFailed(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()
	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "local",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "web", Count: 1}},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	cause := failLocalSandboxCreate(ctx, st, sb.ID, context.DeadlineExceeded)
	if cause == nil {
		t.Fatal("expected original cause to be returned")
	}

	got, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if got.Status != model.SandboxStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.SandboxStatusFailed)
	}
	if !strings.Contains(got.Error, context.DeadlineExceeded.Error()) {
		t.Fatalf("error = %q, want %q", got.Error, context.DeadlineExceeded)
	}
}

func TestFailLocalSandboxCreate_LeavesMissingSandboxAlone(t *testing.T) {
	t.Parallel()

	err := failLocalSandboxCreate(context.Background(), store.NewMemoryStore(), "missing", context.DeadlineExceeded)
	if err == nil {
		t.Fatal("expected original cause to be returned")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("error = %q, want original cause", err)
	}
}
