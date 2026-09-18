package agentsdk

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/diagnostics"
)

func TestClientSessionDispatchesOnDemandLogsSinceTimestamp(t *testing.T) {
	store := diagnostics.NewMemoryStore()
	// Anchored to time.Now, not a fixed date: diagnostics.MemoryStore prunes
	// on every Append using its own real clock against DefaultMaxAge (14
	// days), and this package has no way to inject a fixed one (the field
	// is unexported, diagnostics-package-internal). A fixed past date ages
	// out of retention 14 days later and this test starts failing with no
	// code change involved -- reproduced for real on 2026-09-18, the fixed
	// 2026-09-03 date this replaced having aged out days earlier. See the
	// identical, already-documented pattern in AGENTS.md's Lessons Learned
	// and internal/server/api_diagnostics_test.go's sibling tests.
	now := time.Now().UTC()
	if err := store.Append(context.Background(), diagnostics.Event{
		Timestamp: now.Add(-2 * time.Minute), Component: "agent", Message: "old",
	}); err != nil {
		t.Fatalf("append old event: %v", err)
	}
	if err := store.Append(context.Background(), diagnostics.Event{
		Timestamp: now, Component: "agent", Message: "new",
	}); err != nil {
		t.Fatalf("append new event: %v", err)
	}

	stream := newFakeClientStream()
	session := &clientSession{stream: stream}
	done := make(chan error, 1)
	go func() { done <- session.dispatchCommands(context.Background(), nil, store, nil) }()

	stream.recvCh <- &boxyagentv1.ServerMessage{Payload: &boxyagentv1.ServerMessage_LogRequest{
		LogRequest: &boxyagentv1.LogRequest{
			RequestId:     "pull-1",
			SinceUnixNano: now.Add(-time.Minute).UnixNano(),
			Limit:         10,
		},
	}}

	select {
	case msg := <-stream.sentCh:
		batch := msg.GetLogBatch()
		if batch == nil {
			t.Fatalf("response = %#v, want log batch", msg)
		}
		if batch.GetRequestId() != "pull-1" {
			t.Fatalf("request id = %q, want pull-1", batch.GetRequestId())
		}
		if len(batch.GetEvents()) != 1 || batch.GetEvents()[0].GetMessage() != "new" {
			t.Fatalf("events = %#v, want only the event after since", batch.GetEvents())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for requested log batch")
	}

	stream.close()
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatalf("dispatch error = %v, want EOF", err)
	}
}
