// Package diagnostics provides bounded, redacted operational log storage.
package diagnostics

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultMaxBytes = 10 << 20
	DefaultMaxAge   = 7 * 24 * time.Hour
	DefaultLimit    = 100
	HardMaxLimit    = 1000
	maxMessageBytes = 4096
	maxFieldBytes   = 256
)

// Event is the safe, structured representation exposed by diagnostics.
// Fields not represented here must never cross the diagnostics boundary.
type Event struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Component string    `json:"component,omitempty"`
	Message   string    `json:"message"`
	Pool      string    `json:"pool,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Resource  string    `json:"resource,omitempty"`
	Request   string    `json:"request,omitempty"`
}

// Query selects a bounded page of diagnostic events. Cursor values are opaque
// to callers and are produced by Page.NextCursor.
type Query struct {
	Since     time.Time
	Level     string
	Component string
	Pool      string
	Agent     string
	Resource  string
	Limit     int
	Cursor    string
}

type Page struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type Store interface {
	Append(context.Context, Event) error
	Query(context.Context, Query) (Page, error)
}

// QueryAudit is deliberately limited to safe query metadata.
type QueryAudit struct {
	Actor       string
	Since       string
	Level       string
	Component   string
	Pool        string
	Agent       string
	Resource    string
	Limit       int
	ResultCount int
}

type AuditSink interface {
	RecordDiagnosticsQuery(context.Context, QueryAudit) error
}

// ResourceCleanupAudit describes safe metadata for an administrator cleanup
// mutation. It intentionally contains counts and IDs only; callers must not
// attach resource properties or provider credentials.
type ResourceCleanupAudit struct {
	Actor         string `json:"actor"`
	Mode          string `json:"mode"`
	Force         bool   `json:"force"`
	State         string `json:"state"`
	Unreferenced  bool   `json:"unreferenced"`
	OlderThan     string `json:"older_than,omitempty"`
	CandidateCount int   `json:"candidate_count"`
	CleanedCount   int   `json:"cleaned_count"`
	SkippedCount   int   `json:"skipped_count"`
	ErrorCount     int   `json:"error_count"`
}

// ResourceCleanupAuditSink is optional so existing embedders with an audit
// sink that predates cleanup remain source-compatible.
type ResourceCleanupAuditSink interface {
	RecordResourceCleanup(context.Context, ResourceCleanupAudit) error
}

// FileStore is a bounded JSONL store. It reads the file for each query so a
// second daemon process or a restart sees the same durable snapshot.
type FileStore struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxAge   time.Duration
	now      func() time.Time
}

func NewFileStore(path string, maxBytes int64, maxAge time.Duration) (*FileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("diagnostics store path is required")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	return &FileStore{path: path, maxBytes: maxBytes, maxAge: maxAge, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *FileStore) Append(_ context.Context, event Event) error {
	if s == nil {
		return errors.New("diagnostics store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event = normalizeEvent(event, s.currentTime())
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode diagnostic event: %w", err)
	}
	line = append(line, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create diagnostics directory: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open diagnostics store: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("protect diagnostics store: %w", err)
	}
	_, writeErr := f.Write(line)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("append diagnostic event: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close diagnostics store: %w", closeErr)
	}
	if err := s.compactLocked(); err != nil {
		return err
	}
	return nil
}

func (s *FileStore) Query(_ context.Context, query Query) (Page, error) {
	if s == nil {
		return Page{}, errors.New("diagnostics store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events, err := s.readLocked()
	if err != nil {
		return Page{}, err
	}
	// Apply retention at read time as well as append time. This keeps an
	// existing store fail-closed after a restart, before the next log write has
	// had a chance to compact stale records on disk.
	events = retainEvents(events, s.currentTime(), s.maxAge, s.maxBytes)
	return pageForEvents(events, query)
}

func (s *FileStore) currentTime() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *FileStore) readLocked() ([]Event, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open diagnostics store: %w", err)
	}
	defer func() { _ = f.Close() }()
	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 128<<10)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode diagnostics event: %w", err)
		}
		events = append(events, normalizeEvent(event, event.Timestamp))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read diagnostics store: %w", err)
	}
	return events, nil
}

func (s *FileStore) compactLocked() error {
	events, err := s.readLocked()
	if err != nil {
		return err
	}
	events = retainEvents(events, s.currentTime(), s.maxAge, s.maxBytes)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create diagnostics directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".diagnostics-*.tmp")
	if err != nil {
		return fmt.Errorf("create diagnostics temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect diagnostics temp file: %w", err)
	}
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			_ = tmp.Close()
			return fmt.Errorf("encode compacted diagnostic event: %w", err)
		}
		line = append(line, '\n')
		if _, err := tmp.Write(line); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("write compacted diagnostics: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close compacted diagnostics: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace diagnostics store: %w", err)
	}
	return nil
}

// MemoryStore provides the same bounded query semantics for tests and
// embedders that do not need restart persistence.
type MemoryStore struct {
	mu     sync.Mutex
	events []Event
	now    func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{now: func() time.Time { return time.Now().UTC() }}
}

func (s *MemoryStore) Append(_ context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event = normalizeEvent(event, s.nowTime())
	s.events = append(s.events, event)
	s.events = retainEvents(s.events, s.nowTime(), DefaultMaxAge, DefaultMaxBytes)
	return nil
}

func (s *MemoryStore) Query(_ context.Context, query Query) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pageForEvents(append([]Event(nil), s.events...), query)
}

func (s *MemoryStore) nowTime() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

type FileAuditStore struct{ store *FileStore }

func NewFileAuditStore(path string) (*FileAuditStore, error) {
	store, err := NewFileStore(path, 1<<20, 30*24*time.Hour)
	if err != nil {
		return nil, err
	}
	return &FileAuditStore{store: store}, nil
}

func (s *FileAuditStore) RecordDiagnosticsQuery(ctx context.Context, audit QueryAudit) error {
	filters, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	return s.store.Append(ctx, Event{Component: "audit", Message: string(filters)})
}

func (s *FileAuditStore) RecordResourceCleanup(ctx context.Context, audit ResourceCleanupAudit) error {
	data, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	return s.store.Append(ctx, Event{Component: "audit", Message: string(data)})
}

func normalizeEvent(event Event, now time.Time) Event {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = now.UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.Level = strings.ToUpper(strings.TrimSpace(event.Level))
	if event.Level == "" {
		event.Level = "INFO"
	}
	event.Component = truncate(redactText(strings.TrimSpace(event.Component)), maxFieldBytes)
	event.Message = truncate(redactText(event.Message), maxMessageBytes)
	event.Pool = truncate(redactText(strings.TrimSpace(event.Pool)), maxFieldBytes)
	event.Agent = truncate(redactText(strings.TrimSpace(event.Agent)), maxFieldBytes)
	event.Resource = truncate(redactText(strings.TrimSpace(event.Resource)), maxFieldBytes)
	event.Request = truncate(redactText(strings.TrimSpace(event.Request)), maxFieldBytes)
	return event
}

func pageForEvents(events []Event, query Query) (Page, error) {
	limit := query.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > HardMaxLimit {
		return Page{}, fmt.Errorf("diagnostics limit must be between 1 and %d", HardMaxLimit)
	}
	var cursor cursorValue
	var err error
	if query.Cursor != "" {
		cursor, err = decodeCursor(query.Cursor)
		if err != nil {
			return Page{}, err
		}
	}
	sort.Slice(events, func(i, j int) bool { return newer(events[i], events[j]) })
	filtered := make([]Event, 0, len(events))
	for _, event := range events {
		if !query.Since.IsZero() && event.Timestamp.Before(query.Since) {
			continue
		}
		if query.Level != "" && !strings.EqualFold(event.Level, query.Level) {
			continue
		}
		if query.Component != "" && event.Component != query.Component {
			continue
		}
		if query.Pool != "" && event.Pool != query.Pool {
			continue
		}
		if query.Agent != "" && event.Agent != query.Agent {
			continue
		}
		if query.Resource != "" && event.Resource != query.Resource {
			continue
		}
		if query.Cursor != "" && !afterCursor(event, cursor) {
			continue
		}
		filtered = append(filtered, event)
	}
	page := Page{}
	if len(filtered) > limit {
		page.Events = append([]Event(nil), filtered[:limit]...)
		page.NextCursor = encodeCursor(filtered[limit-1])
	} else {
		page.Events = append([]Event(nil), filtered...)
	}
	if page.Events == nil {
		page.Events = []Event{}
	}
	return page, nil
}

func retainEvents(events []Event, now time.Time, maxAge time.Duration, maxBytes int64) []Event {
	cutoff := now.Add(-maxAge)
	sort.Slice(events, func(i, j int) bool { return newer(events[i], events[j]) })
	kept := make([]Event, 0, len(events))
	var bytes int64
	for _, event := range events {
		if event.Timestamp.Before(cutoff) {
			continue
		}
		line, _ := json.Marshal(event)
		lineBytes := int64(len(line) + 1)
		if len(kept) > 0 && bytes+lineBytes > maxBytes {
			break
		}
		kept = append(kept, event)
		bytes += lineBytes
	}
	return kept
}

func newer(a, b Event) bool {
	if a.Timestamp.Equal(b.Timestamp) {
		return a.ID > b.ID
	}
	return a.Timestamp.After(b.Timestamp)
}

type cursorValue struct {
	Timestamp time.Time
	ID        string
}

func encodeCursor(event Event) string {
	return base64.RawURLEncoding.EncodeToString([]byte(event.Timestamp.Format(time.RFC3339Nano) + "\x00" + event.ID))
}

func decodeCursor(value string) (cursorValue, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursorValue{}, errors.New("invalid diagnostics cursor")
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return cursorValue{}, errors.New("invalid diagnostics cursor")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return cursorValue{}, errors.New("invalid diagnostics cursor")
	}
	return cursorValue{Timestamp: timestamp, ID: parts[1]}, nil
}

func afterCursor(event Event, cursor cursorValue) bool {
	return event.Timestamp.Before(cursor.Timestamp) || (event.Timestamp.Equal(cursor.Timestamp) && event.ID < cursor.ID)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

// redactText is split from normalizeEvent so all persistence paths share
// exactly the same redaction boundary.
func redactText(value string) string {
	return RedactText(value)
}
