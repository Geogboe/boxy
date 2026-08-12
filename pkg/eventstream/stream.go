// Package eventstream provides generic primitives for bounded event streaming.
// It deliberately contains no provider, workflow, REST, or transport logic.
package eventstream

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
)

var (
	ErrCompleted     = errors.New("stream already completed")
	ErrLimitExceeded = errors.New("stream output limit exceeded")
	ErrInvalidStream = errors.New("invalid output stream")
)

// Channel identifies a logical event channel. The package does not prescribe
// channel names; consumers may use stdout/stderr, progress/log, or any other
// workflow-specific vocabulary.
type Channel string

// EventKind identifies the lifecycle event represented by an Event.
type EventKind uint8

const (
	Data EventKind = iota + 1
	Complete
)

// Completion is the terminal outcome supplied with a Complete event. Attributes
// carry consumer-defined result metadata without coupling this package to a
// particular workflow.
type Completion struct {
	Attributes map[string]string
	Err        error
}

// Event is a provider-neutral stream event. Payload is owned by the event and
// must not be mutated by a sink after Send returns.
type Event struct {
	Kind       EventKind
	Channel    Channel
	Payload    []byte
	Completion *Completion
}

// Sink receives events. Implementations may block to apply backpressure; the
// publisher propagates that behavior and observes context cancellation.
type Sink interface {
	Send(context.Context, Event) error
}

// Limits bounds each emitted payload and the aggregate data sent by a
// Publisher. A zero limit means unlimited for that dimension.
type Limits struct {
	MaxChunkBytes int
	MaxTotalBytes int64
}

// Publisher validates, bounds, and forwards stream events to a Sink.
type Publisher struct {
	mu        sync.Mutex
	sink      Sink
	limits    Limits
	total     int64
	completed bool
}

// NewPublisher creates a bounded publisher. A nil sink is rejected when the
// first event is emitted so construction remains convenient for composition.
func NewPublisher(sink Sink, limits Limits) *Publisher {
	return &Publisher{sink: sink, limits: limits}
}

// Write emits data, splitting it into bounded chunks when needed.
func (p *Publisher) Write(ctx context.Context, channel Channel, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed {
		return ErrCompleted
	}
	if p.sink == nil {
		return fmt.Errorf("%w: nil sink", ErrInvalidStream)
	}
	if channel == "" {
		return fmt.Errorf("%w: empty channel", ErrInvalidStream)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if p.limits.MaxTotalBytes > 0 && p.total+int64(len(data)) > p.limits.MaxTotalBytes {
		return ErrLimitExceeded
	}
	chunkSize := len(data)
	if p.limits.MaxChunkBytes > 0 && chunkSize > p.limits.MaxChunkBytes {
		chunkSize = p.limits.MaxChunkBytes
	}
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		payload := append([]byte(nil), data[offset:end]...)
		if err := p.sink.Send(ctx, Event{Kind: Data, Channel: channel, Payload: payload}); err != nil {
			return err
		}
		p.total += int64(len(payload))
	}
	return nil
}

// Complete emits the terminal event exactly once.
func (p *Publisher) Complete(ctx context.Context, result Completion) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed {
		return ErrCompleted
	}
	if p.sink == nil {
		return fmt.Errorf("%w: nil sink", ErrInvalidStream)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.sink.Send(ctx, Event{
		Kind:       Complete,
		Completion: &Completion{Attributes: cloneAttributes(result.Attributes), Err: result.Err},
	}); err != nil {
		return err
	}
	p.completed = true
	return nil
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	return maps.Clone(attributes)
}
