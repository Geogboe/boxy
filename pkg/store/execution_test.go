package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestExecutionStoreRoundTripAndOutputIsolation(t *testing.T) {
	for name, newStore := range map[string]func(t *testing.T) store.Store{
		"memory": func(t *testing.T) store.Store { return store.NewMemoryStore() },
		"disk": func(t *testing.T) store.Store {
			s, err := store.NewDiskStore(t.TempDir() + "/state.json")
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := newStore(t)
			now := time.Now().UTC()
			input := model.Execution{
				ID: "exec-1", SandboxID: "sb-1", ResourceID: "res-1",
				Status: model.ExecutionStatusRunning, InputKind: model.ExecutionInputCommand,
				RequestFingerprint: "fingerprint", CreatedAt: now, DeadlineAt: now.Add(time.Minute),
				Chunks: []model.ExecutionChunk{{Cursor: 1, Stream: "stdout", Data: []byte("hello")}},
			}
			if err := st.PutExecution(context.Background(), input); err != nil {
				t.Fatalf("PutExecution: %v", err)
			}
			got, err := st.GetExecution(context.Background(), input.ID)
			if err != nil {
				t.Fatalf("GetExecution: %v", err)
			}
			got.Chunks[0].Data[0] = 'X'
			again, err := st.GetExecution(context.Background(), input.ID)
			if err != nil {
				t.Fatalf("GetExecution again: %v", err)
			}
			if string(again.Chunks[0].Data) != "hello" {
				t.Fatalf("store returned mutable output alias: %q", again.Chunks[0].Data)
			}
		})
	}
}
