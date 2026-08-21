package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Geogboe/boxy/pkg/lifecycle"
)

var _ lifecycle.EventStore = (*MemoryStore)(nil)
var _ lifecycle.EventStore = (*DiskStore)(nil)

func (s *MemoryStore) Append(_ context.Context, event lifecycle.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateLifecycleEvent(event); err != nil {
		return err
	}
	if existing, ok := s.events[event.ID]; ok {
		if existing.Event.Type != event.Type || existing.Event.Subject != event.Subject || string(existing.Event.Payload) != string(event.Payload) {
			return fmt.Errorf("lifecycle event %q already exists with different content", event.ID)
		}
		return nil
	}
	s.events[event.ID] = lifecycle.Record{Event: event, Status: lifecycle.StatusPending}
	return nil
}

func (s *MemoryStore) Claim(_ context.Context, now time.Time, lease time.Duration) (lifecycle.Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return claimEvent(s.events, now, lease)
}

func (s *MemoryStore) Ack(_ context.Context, claim lifecycle.Claim) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ackEvent(s.events, claim, time.Now().UTC())
}

func (s *MemoryStore) Retry(_ context.Context, claim lifecycle.Claim, availableAt time.Time, cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return retryEvent(s.events, claim, availableAt, cause)
}

func (s *MemoryStore) Fail(_ context.Context, claim lifecycle.Claim, cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return failEvent(s.events, claim, cause, time.Now().UTC())
}

func (s *MemoryStore) Compact(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	compactEvents(s.events, before)
	return nil
}

func (s *DiskStore) Append(_ context.Context, event lifecycle.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateLifecycleEvent(event); err != nil {
		return err
	}
	if existing, ok := s.data.Events[event.ID]; ok {
		if existing.Event.Type != event.Type || existing.Event.Subject != event.Subject || string(existing.Event.Payload) != string(event.Payload) {
			return fmt.Errorf("lifecycle event %q already exists with different content", event.ID)
		}
		return nil
	}
	s.data.Events[event.ID] = lifecycle.Record{Event: event, Status: lifecycle.StatusPending}
	return s.persistLocked()
}

func (s *DiskStore) Claim(_ context.Context, now time.Time, lease time.Duration) (lifecycle.Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, err := claimEvent(s.data.Events, now, lease)
	if err != nil {
		return lifecycle.Claim{}, err
	}
	if err := s.persistLocked(); err != nil {
		return lifecycle.Claim{}, err
	}
	return claim, nil
}

func (s *DiskStore) Ack(_ context.Context, claim lifecycle.Claim) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ackEvent(s.data.Events, claim, time.Now().UTC()); err != nil {
		return err
	}
	return s.persistLocked()
}

func (s *DiskStore) Retry(_ context.Context, claim lifecycle.Claim, availableAt time.Time, cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := retryEvent(s.data.Events, claim, availableAt, cause); err != nil {
		return err
	}
	return s.persistLocked()
}

func (s *DiskStore) Fail(_ context.Context, claim lifecycle.Claim, cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := failEvent(s.data.Events, claim, cause, time.Now().UTC()); err != nil {
		return err
	}
	return s.persistLocked()
}

func (s *DiskStore) Compact(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	compactEvents(s.data.Events, before)
	return s.persistLocked()
}

func validateLifecycleEvent(event lifecycle.Event) error {
	if event.ID == "" || event.Type == "" || event.Subject == "" {
		return fmt.Errorf("lifecycle event requires id, type, and subject")
	}
	return nil
}

func claimEvent(records map[string]lifecycle.Record, now time.Time, lease time.Duration) (lifecycle.Claim, error) {
	if lease <= 0 {
		return lifecycle.Claim{}, fmt.Errorf("lifecycle lease must be positive")
	}
	for _, id := range lifecycle.SortRecordsByID(records) {
		record := records[id]
		available := record.Status == lifecycle.StatusPending && !record.Event.AvailableAt.After(now)
		reclaim := record.Status == lifecycle.StatusLeased && !record.LeaseUntil.After(now)
		if !available && !reclaim {
			continue
		}
		record.Status = lifecycle.StatusLeased
		record.Event.Attempt++
		record.LeaseToken = uuid.NewString()
		record.LeaseUntil = now.Add(lease).UTC()
		record.LastError = ""
		record.Event.Payload = append([]byte(nil), record.Event.Payload...)
		record.Event.AvailableAt = record.Event.AvailableAt.UTC()
		record.Event.RecordedAt = record.Event.RecordedAt.UTC()
		records[id] = record
		return lifecycle.Claim{Event: record.Event, LeaseToken: record.LeaseToken, LeaseUntil: record.LeaseUntil}, nil
	}
	return lifecycle.Claim{}, lifecycle.ErrNoWork
}

func getClaimed(records map[string]lifecycle.Record, claim lifecycle.Claim) (*lifecycle.Record, error) {
	record, ok := records[claim.Event.ID]
	if !ok || record.Status != lifecycle.StatusLeased || record.LeaseToken != claim.LeaseToken {
		return nil, lifecycle.ErrLeaseLost
	}
	return &record, nil
}

func ackEvent(records map[string]lifecycle.Record, claim lifecycle.Claim, now time.Time) error {
	record, err := getClaimed(records, claim)
	if err != nil {
		return err
	}
	record.Status = lifecycle.StatusAcked
	record.Completed = now.UTC()
	record.LeaseToken = ""
	record.LeaseUntil = time.Time{}
	records[claim.Event.ID] = *record
	return nil
}

func retryEvent(records map[string]lifecycle.Record, claim lifecycle.Claim, availableAt time.Time, cause error) error {
	record, err := getClaimed(records, claim)
	if err != nil {
		return err
	}
	record.Status = lifecycle.StatusPending
	record.Event.AvailableAt = availableAt.UTC()
	record.LastError = errorString(cause)
	record.LeaseToken = ""
	record.LeaseUntil = time.Time{}
	records[claim.Event.ID] = *record
	return nil
}

func failEvent(records map[string]lifecycle.Record, claim lifecycle.Claim, cause error, now time.Time) error {
	record, err := getClaimed(records, claim)
	if err != nil {
		return err
	}
	record.Status = lifecycle.StatusFailed
	record.LastError = errorString(cause)
	record.Completed = now.UTC()
	record.LeaseToken = ""
	record.LeaseUntil = time.Time{}
	records[claim.Event.ID] = *record
	return nil
}

func compactEvents(records map[string]lifecycle.Record, before time.Time) {
	for id, record := range records {
		if (record.Status == lifecycle.StatusAcked || record.Status == lifecycle.StatusFailed) && !record.Completed.IsZero() && record.Completed.Before(before) {
			delete(records, id)
		}
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
