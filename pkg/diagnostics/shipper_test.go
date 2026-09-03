package diagnostics

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

type recordingBatchSink struct {
	batches [][]Event
	err     error
}

func (s *recordingBatchSink) Ship(_ context.Context, events []Event) error {
	if s.err != nil {
		return s.err
	}
	s.batches = append(s.batches, append([]Event(nil), events...))
	return nil
}

func TestShipperFlushesBoundedBatchesAndRetriesFailures(t *testing.T) {
	shipper := NewShipper(ShipperOptions{MaxBatch: 2, MaxQueue: 4})
	for i := 0; i < 3; i++ {
		if err := shipper.Submit(context.Background(), Event{Message: "event"}); err != nil {
			t.Fatalf("Submit[%d]: %v", i, err)
		}
	}

	sink := &recordingBatchSink{err: errors.New("temporary")}
	if err := shipper.Flush(context.Background(), sink); err == nil {
		t.Fatal("Flush succeeded through sink failure")
	}
	if got := shipper.Pending(); got != 3 {
		t.Fatalf("pending after failed flush = %d, want 3", got)
	}
	sink.err = nil
	if err := shipper.Flush(context.Background(), sink); err != nil {
		t.Fatalf("retry Flush: %v", err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0]) != 2 || shipper.Pending() != 1 {
		t.Fatalf("batches=%+v pending=%d, want one batch of two and one pending", sink.batches, shipper.Pending())
	}
}

func TestShipperHandlerSubmitsSanitizedEvents(t *testing.T) {
	shipper := NewShipper(ShipperOptions{MaxBatch: 4, MaxQueue: 4})
	logger := slog.New(shipper.Handler(slog.NewTextHandler(discardWriter{}, nil)))
	logger.Error("agent failed", "component", "agent", "password", "secret", "hostname", "host.example.test")

	sink := &recordingBatchSink{}
	if err := shipper.Flush(context.Background(), sink); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0]) != 1 {
		t.Fatalf("batches = %+v, want one event", sink.batches)
	}
	message := sink.batches[0][0].Message
	if message != "agent failed" || sink.batches[0][0].Component != "agent" {
		t.Fatalf("event = %+v, want safe message and component", sink.batches[0][0])
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
