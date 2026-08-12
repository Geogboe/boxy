package eventstream

import (
	"context"
	"errors"
	"testing"
)

type recordingSink struct {
	events []Event
}

func (s *recordingSink) Send(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}

func TestPublisherSplitsChunksAndCompletesOnce(t *testing.T) {
	sink := new(recordingSink)
	publisher := NewPublisher(sink, Limits{MaxChunkBytes: 2, MaxTotalBytes: 5})

	if err := publisher.Write(context.Background(), Channel("stdout"), []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := publisher.Complete(context.Background(), Completion{Attributes: map[string]string{"exit_code": "7"}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := publisher.Complete(context.Background(), Completion{Attributes: map[string]string{"exit_code": "8"}}); !errors.Is(err, ErrCompleted) {
		t.Fatalf("second Complete = %v, want ErrCompleted", err)
	}

	want := []Event{
		{Kind: Data, Channel: Channel("stdout"), Payload: []byte("he")},
		{Kind: Data, Channel: Channel("stdout"), Payload: []byte("ll")},
		{Kind: Data, Channel: Channel("stdout"), Payload: []byte("o")},
		{Kind: Complete, Completion: &Completion{Attributes: map[string]string{"exit_code": "7"}}},
	}
	if len(sink.events) != len(want) {
		t.Fatalf("event count = %d, want %d", len(sink.events), len(want))
	}
	for i := range want {
		got := sink.events[i]
		if got.Kind != want[i].Kind || got.Channel != want[i].Channel || string(got.Payload) != string(want[i].Payload) {
			t.Fatalf("event %d = %#v, want %#v", i, got, want[i])
		}
		if i == len(want)-1 && (got.Completion == nil || got.Completion.Attributes["exit_code"] != "7") {
			t.Fatalf("completion event = %#v, want exit_code 7", got.Completion)
		}
	}
}

func TestPublisherEnforcesTotalLimit(t *testing.T) {
	sink := new(recordingSink)
	publisher := NewPublisher(sink, Limits{MaxChunkBytes: 10, MaxTotalBytes: 3})
	if err := publisher.Write(context.Background(), Channel("stderr"), []byte("toolong")); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Write over limit = %v, want ErrLimitExceeded", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("events after rejected write = %#v, want none", sink.events)
	}
}

func TestPublisherRejectsNilSink(t *testing.T) {
	publisher := NewPublisher(nil, Limits{})
	if err := publisher.Write(context.Background(), Channel("stdout"), []byte("x")); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("Write with nil sink = %v, want ErrInvalidStream", err)
	}
}

func TestPublisherPropagatesCancellation(t *testing.T) {
	sink := &blockingSink{}
	publisher := NewPublisher(sink, Limits{MaxChunkBytes: 10})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := publisher.Write(ctx, Channel("stdout"), []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write cancelled = %v, want context.Canceled", err)
	}
}

type blockingSink struct{}

func (*blockingSink) Send(ctx context.Context, _ Event) error {
	return ctx.Err()
}
