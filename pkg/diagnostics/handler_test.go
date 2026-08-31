package diagnostics

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type captureStore struct{ events []Event }

func (s *captureStore) Append(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *captureStore) Query(context.Context, Query) (Page, error) { return Page{}, nil }

func TestHandlerStoresOnlySafeRedactedFields(t *testing.T) {
	store := &captureStore{}
	logger := slog.New(NewHandler(slog.NewTextHandler(io.Discard, nil), store))
	logger.Error("request failed: Authorization: Bearer ${BOXY_TEST_API_KEY} https://boxy.example.test/?token=${BOXY_TEST_TOKEN}",
		"component", "reconcile", "pool", "pool-a", "agent", "agent-a", "resource", "resource-a",
		"authorization", "${BOXY_TEST_API_KEY}", "command", "do-not-store")

	if len(store.events) != 1 {
		t.Fatalf("events = %+v, want one event", store.events)
	}
	event := store.events[0]
	if event.Level != "ERROR" || event.Component != "reconcile" || event.Pool != "pool-a" || event.Agent != "agent-a" || event.Resource != "resource-a" {
		t.Fatalf("event = %+v, missing safe fields", event)
	}
	if strings.Contains(event.Message, "${BOXY_TEST_API_KEY}") || strings.Contains(event.Message, "${BOXY_TEST_TOKEN}") {
		t.Fatalf("message leaked secret: %q", event.Message)
	}
	if strings.Contains(event.Message, "do-not-store") {
		t.Fatalf("message unexpectedly contains omitted command: %q", event.Message)
	}
}
