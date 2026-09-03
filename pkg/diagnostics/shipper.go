package diagnostics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
)

const (
	defaultShipperBatch = 32
	defaultShipperQueue = 256
)

var ErrShipperQueueFull = errors.New("diagnostics shipper queue is full")

// BatchSink receives one bounded batch from a Shipper.
type BatchSink interface {
	Ship(context.Context, []Event) error
}

// BatchSinkFunc adapts a function to BatchSink.
type BatchSinkFunc func(context.Context, []Event) error

func (f BatchSinkFunc) Ship(ctx context.Context, events []Event) error {
	return f(ctx, events)
}

// ShipperOptions bounds memory and transport work for a log source.
type ShipperOptions struct {
	MaxBatch int
	MaxQueue int
}

// Shipper is a bounded, retryable batch buffer. Submit never sends network
// traffic; callers choose when and where Flush sends the batch.
type Shipper struct {
	mu       sync.Mutex
	flushMu  sync.Mutex
	queue    []Event
	maxBatch int
	maxQueue int
	sanitize *Sanitizer
}

// NewShipper creates a bounded log shipper.
func NewShipper(options ShipperOptions) *Shipper {
	if options.MaxBatch <= 0 {
		options.MaxBatch = defaultShipperBatch
	}
	if options.MaxQueue <= 0 {
		options.MaxQueue = defaultShipperQueue
	}
	return &Shipper{maxBatch: options.MaxBatch, maxQueue: options.MaxQueue, sanitize: NewSanitizer()}
}

// Submit sanitizes and queues one event. Queue-full is explicit so callers
// can count drops; the slog adapter intentionally treats it as best effort.
func (s *Shipper) Submit(ctx context.Context, event Event) error {
	if s == nil {
		return errors.New("diagnostics shipper is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) >= s.maxQueue {
		return ErrShipperQueueFull
	}
	s.queue = append(s.queue, s.sanitize.Event(event))
	return nil
}

// Flush sends at most MaxBatch events. A failed send leaves the batch queued
// at the front for a later retry.
func (s *Shipper) Flush(ctx context.Context, sink BatchSink) error {
	if s == nil {
		return errors.New("diagnostics shipper is nil")
	}
	if sink == nil {
		return errors.New("diagnostics shipper sink is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	n := len(s.queue)
	if n > s.maxBatch {
		n = s.maxBatch
	}
	batch := append([]Event(nil), s.queue[:n]...)
	s.mu.Unlock()
	if n == 0 {
		return nil
	}
	if err := sink.Ship(ctx, batch); err != nil {
		return err
	}
	s.mu.Lock()
	s.queue = append([]Event(nil), s.queue[n:]...)
	s.mu.Unlock()
	return nil
}

// Pending returns the number of events waiting to be shipped.
func (s *Shipper) Pending() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Handler returns a slog handler that forwards safe events to next and queues
// a sanitized copy for the shipper. The next handler may be slog.Discard's
// equivalent when an agent has no local log destination.
func (s *Shipper) Handler(next slog.Handler) slog.Handler {
	if next == nil {
		next = slog.NewTextHandler(io.Discard, nil)
	}
	return &shipperHandler{next: next, shipper: s}
}

type shipperHandler struct {
	next    slog.Handler
	shipper *Shipper
	attrs   []slog.Attr
	groups  []string
}

func (h *shipperHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *shipperHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.next.Handle(ctx, record)
	_ = h.shipper.Submit(ctx, eventFromSlog(record, h.attrs, h.groups))
	return err
}

func (h *shipperHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	clone.next = h.next.WithAttrs(attrs)
	return &clone
}

func (h *shipperHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	clone.next = h.next.WithGroup(name)
	return &clone
}
