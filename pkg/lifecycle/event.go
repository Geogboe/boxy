// Package lifecycle provides generic event-driven lifecycle primitives.
//
// It is intentionally separate from policycontroller, which reconciles a
// desired state synchronously, and eventstream, which carries bounded live
// command output.
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNoWork       = errors.New("no lifecycle work available")
	ErrInvalidEvent = errors.New("invalid lifecycle event")
	ErrLeaseLost    = errors.New("lifecycle event lease lost")
)

// Event is the non-secret envelope persisted in the operational event queue.
// Payload must contain identifiers and safe state only; callers must not put
// credentials or other secret material in it.
type Event struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Subject     string          `json:"subject"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	RecordedAt  time.Time       `json:"recorded_at"`
	AvailableAt time.Time       `json:"available_at"`
	Attempt     int             `json:"attempt"`
}

// NewEvent creates an event with a generated ID and server receipt time.
func NewEvent(eventType, subject string, payload json.RawMessage, now time.Time) Event {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return Event{
		ID:          uuid.NewString(),
		Type:        strings.TrimSpace(eventType),
		Subject:     strings.TrimSpace(subject),
		Payload:     append(json.RawMessage(nil), payload...),
		RecordedAt:  now.UTC(),
		AvailableAt: now.UTC(),
	}
}

// Status is the durable queue state of an event.
type Status string

const (
	StatusPending Status = "pending"
	StatusLeased  Status = "leased"
	StatusAcked   Status = "acked"
	StatusFailed  Status = "failed"
)

// Record is the persisted queue representation. EventStore implementations
// may persist this directly in their native format.
type Record struct {
	Event      Event     `json:"event"`
	Status     Status    `json:"status"`
	LeaseToken string    `json:"lease_token,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	Completed  time.Time `json:"completed,omitempty"`
}

// Claim identifies a leased event and prevents a stale worker from
// acknowledging or rescheduling a lease it no longer owns.
type Claim struct {
	Event      Event
	LeaseToken string
	LeaseUntil time.Time
}

// EventStore is the narrow durable queue contract used by Dispatcher.
type EventStore interface {
	Append(context.Context, Event) error
	Claim(context.Context, time.Time, time.Duration) (Claim, error)
	Ack(context.Context, Claim) error
	Retry(context.Context, Claim, time.Time, error) error
	Fail(context.Context, Claim, error) error
	Compact(context.Context, time.Time) error
}

// Outcome tells Dispatcher how to record a handler result.
type Outcome string

const (
	OutcomeAck      Outcome = "ack"
	OutcomeRetry    Outcome = "retry"
	OutcomeTerminal Outcome = "terminal"
)

// Handler evaluates an event and performs the policy-selected action.
type Handler interface {
	Handle(context.Context, Event) (Outcome, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, Event) (Outcome, error)

func (f HandlerFunc) Handle(ctx context.Context, event Event) (Outcome, error) {
	return f(ctx, event)
}

// Dispatcher publishes and delivers lifecycle events with at-least-once
// semantics. Handlers must be idempotent because a process can exit after an
// action succeeds and before its acknowledgement is persisted.
type Dispatcher struct {
	Events        EventStore
	Handler       Handler
	Clock         func() time.Time
	LeaseDuration time.Duration
	RetryDelay    time.Duration
	CompactAfter  time.Duration
}

// NewDispatcher constructs a dispatcher with conservative operational
// defaults. The queue remains usable without a background goroutine through
// RunOnce, which keeps integration tests and one-shot tools deterministic.
func NewDispatcher(events EventStore, handler Handler) *Dispatcher {
	return &Dispatcher{
		Events:        events,
		Handler:       handler,
		Clock:         func() time.Time { return time.Now().UTC() },
		LeaseDuration: 2 * time.Minute,
		RetryDelay:    10 * time.Second,
		CompactAfter:  24 * time.Hour,
	}
}

func (d *Dispatcher) now() time.Time {
	if d.Clock != nil {
		return d.Clock().UTC()
	}
	return time.Now().UTC()
}

// Publish appends an event. An existing event with the same ID is treated as
// an idempotent duplicate when its envelope matches.
func (d *Dispatcher) Publish(ctx context.Context, event Event) error {
	if d == nil || d.Events == nil {
		return fmt.Errorf("lifecycle event store is required")
	}
	if event.RecordedAt.IsZero() {
		event.RecordedAt = d.now()
	}
	if event.AvailableAt.IsZero() {
		event.AvailableAt = event.RecordedAt
	}
	if err := validateEvent(event); err != nil {
		return err
	}
	return d.Events.Append(ctx, event)
}

// RunOnce claims and processes one available event. It returns false without
// error when the queue is empty.
func (d *Dispatcher) RunOnce(ctx context.Context) (bool, error) {
	if d == nil || d.Events == nil {
		return false, fmt.Errorf("lifecycle event store is required")
	}
	if d.Handler == nil {
		return false, fmt.Errorf("lifecycle event handler is required")
	}
	now := d.now()
	leaseDuration := d.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 2 * time.Minute
	}
	claim, err := d.Events.Claim(ctx, now, leaseDuration)
	if errors.Is(err, ErrNoWork) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	outcome, handleErr := d.Handler.Handle(ctx, claim.Event)
	switch outcome {
	case OutcomeAck:
		if handleErr != nil {
			return true, d.Events.Retry(ctx, claim, now.Add(d.retryDelay()), handleErr)
		}
		return true, d.Events.Ack(ctx, claim)
	case OutcomeTerminal:
		return true, d.Events.Fail(ctx, claim, handleErr)
	case OutcomeRetry:
		if handleErr == nil {
			handleErr = errors.New("lifecycle handler requested retry")
		}
		return true, d.Events.Retry(ctx, claim, now.Add(d.retryDelay()), handleErr)
	default:
		if handleErr == nil {
			return true, d.Events.Ack(ctx, claim)
		}
		return true, d.Events.Retry(ctx, claim, now.Add(d.retryDelay()), handleErr)
	}
}

// Run delivers events until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	var lastCompact time.Time
	for {
		processed, err := d.RunOnce(ctx)
		if err != nil {
			return err
		}
		if processed {
			continue
		}
		if d.CompactAfter > 0 && (lastCompact.IsZero() || d.now().Sub(lastCompact) >= d.CompactAfter) {
			now := d.now()
			if err := d.Events.Compact(ctx, now.Add(-d.CompactAfter)); err != nil {
				return err
			}
			lastCompact = now
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (d *Dispatcher) retryDelay() time.Duration {
	if d.RetryDelay < 0 {
		return 0
	}
	return d.RetryDelay
}

func validateEvent(event Event) error {
	if strings.TrimSpace(event.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidEvent)
	}
	if strings.TrimSpace(event.Type) == "" {
		return fmt.Errorf("%w: type is required", ErrInvalidEvent)
	}
	if strings.TrimSpace(event.Subject) == "" {
		return fmt.Errorf("%w: subject is required", ErrInvalidEvent)
	}
	return nil
}

// SortRecordsByID provides deterministic ordering for store implementations.
func SortRecordsByID(records map[string]Record) []string {
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
