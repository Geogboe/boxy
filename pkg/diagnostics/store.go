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
	DefaultMaxAge   = 14 * 24 * time.Hour
	DefaultLimit    = 100
	HardMaxLimit    = 1000
	maxMessageBytes = 4096
	maxFieldBytes   = 256
)

// Event is the safe, structured representation exposed by diagnostics.
// Fields not represented here must never cross the diagnostics boundary.
type Event struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Level        string    `json:"level"`
	Component    string    `json:"component,omitempty"`
	Message      string    `json:"message,omitempty"`
	Operation    string    `json:"operation,omitempty"`
	Job          string    `json:"job,omitempty"`
	Step         string    `json:"step,omitempty"`
	Status       string    `json:"status,omitempty"`
	Attempt      int       `json:"attempt,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorSummary string    `json:"error_summary,omitempty"`
	// DurationMS is the elapsed time of the operation/step this event
	// describes, in milliseconds. Zero means "not reported" — most events
	// have no associated duration. See #355.
	DurationMS int64  `json:"duration_ms,omitempty"`
	Pool       string `json:"pool,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Resource   string `json:"resource,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Request    string `json:"request,omitempty"`
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
	Provider  string
	Job       string
	Status    string
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
	Job         string
	Status      string
	Pool        string
	Agent       string
	Resource    string
	Provider    string
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
	Actor          string `json:"actor"`
	Mode           string `json:"mode"`
	Force          bool   `json:"force"`
	State          string `json:"state"`
	Unreferenced   bool   `json:"unreferenced"`
	OlderThan      string `json:"older_than,omitempty"`
	CandidateCount int    `json:"candidate_count"`
	CleanedCount   int    `json:"cleaned_count"`
	SkippedCount   int    `json:"skipped_count"`
	ErrorCount     int    `json:"error_count"`
}

// ResourceCleanupAuditSink is optional so existing embedders with an audit
// sink that predates cleanup remain source-compatible.
type ResourceCleanupAuditSink interface {
	RecordResourceCleanup(context.Context, ResourceCleanupAudit) error
}

// FileStore is a bounded JSONL store. File metadata invalidates cached
// snapshots when another store or process changes the durable history.
type FileStore struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxAge   time.Duration
	now      func() time.Time
	cache    []Event
	cacheKey fileCacheKey
	cacheOK  bool
	// Keep the append snapshot separate: queries can filter expired events
	// without removing them from disk. Fast appends need the disk snapshot.
	appendCache  []Event
	appendKey    fileCacheKey
	appendOK     bool
	appendExpiry time.Time
}

type fileCacheKey struct {
	size    int64
	modTime time.Time
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
	now := s.currentTime()
	event = normalizeEvent(event, now)
	info, err := os.Stat(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat diagnostics store: %w", err)
	}
	key := fileCacheKey{}
	if info != nil {
		key = fileCacheKey{size: info.Size(), modTime: info.ModTime()}
	}
	reload := !s.appendOK || s.appendKey != key
	events := s.appendCache
	if reload {
		events, err = s.readLocked()
		if err != nil {
			return err
		}
	}
	previousCount := len(events)
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode diagnostic event: %w", err)
	}
	expiry := event.Timestamp.Add(s.maxAge)
	fast := !reload && key.size+int64(len(line)+1) <= s.maxBytes && !now.After(expiry) && (previousCount == 0 || !now.After(s.appendExpiry))
	// Invalidate before mutating the snapshot or disk so failures force a reload.
	s.appendOK = false
	s.cacheOK = false
	events = append(events, event)
	retained := events
	if !fast {
		retained = retainEvents(retained, now, s.maxAge, s.maxBytes)
	}
	if len(retained) != previousCount+1 || (reload && previousCount != 0) {
		if err := s.writeRetainedLocked(retained); err != nil {
			return err
		}
	} else if err := s.appendEventLocked(line); err != nil {
		return err
	}
	info, err = os.Stat(s.path)
	if err != nil {
		return fmt.Errorf("stat appended diagnostics: %w", err)
	}
	s.appendCache = retained
	s.appendKey = fileCacheKey{size: info.Size(), modTime: info.ModTime()}
	switch {
	case fast:
		if previousCount == 0 || expiry.Before(s.appendExpiry) {
			s.appendExpiry = expiry
		}
	case len(retained) != 0:
		// Retention orders newest first; the final event expires first.
		s.appendExpiry = retained[len(retained)-1].Timestamp.Add(s.maxAge)
	default:
		s.appendExpiry = time.Time{}
	}
	s.appendOK = true
	return nil
}

func (s *FileStore) appendEventLocked(line []byte) error {
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
	return nil
}

func (s *FileStore) Query(_ context.Context, query Query) (Page, error) {
	if s == nil {
		return Page{}, errors.New("diagnostics store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events, err := s.orderedEventsLocked()
	if err != nil {
		return Page{}, err
	}
	// Apply retention at read time as well as append time. This keeps an
	// existing store fail-closed after a restart, before the next log write has
	// had a chance to compact stale records on disk.
	return pageForOrderedEvents(events, query)
}

func (s *FileStore) orderedEventsLocked() ([]Event, error) {
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.cache, s.cacheOK = []Event{}, true
		s.cacheKey = fileCacheKey{}
		return []Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat diagnostics store: %w", err)
	}
	key := fileCacheKey{size: info.Size(), modTime: info.ModTime()}
	if s.cacheOK && s.cacheKey == key {
		s.cache = retainEvents(s.cache, s.currentTime(), s.maxAge, s.maxBytes)
		return append([]Event(nil), s.cache...), nil
	}
	events, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	events = retainEvents(events, s.currentTime(), s.maxAge, s.maxBytes)
	sort.Slice(events, func(i, j int) bool { return newer(events[i], events[j]) })
	s.cache = append([]Event(nil), events...)
	s.cacheKey = key
	s.cacheOK = true
	return append([]Event(nil), events...), nil
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

func (s *FileStore) writeRetainedLocked(events []Event) error {
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
	writer := bufio.NewWriterSize(tmp, 64<<10)
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			_ = tmp.Close()
			return fmt.Errorf("encode compacted diagnostic event: %w", err)
		}
		line = append(line, '\n')
		if _, err := writer.Write(line); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("write compacted diagnostics: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush compacted diagnostics: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close compacted diagnostics: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace diagnostics store: %w", err)
	}
	s.cacheOK = false
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
	event.Operation = truncate(redactText(strings.TrimSpace(event.Operation)), maxFieldBytes)
	event.Job = truncate(redactText(strings.TrimSpace(event.Job)), maxFieldBytes)
	event.Step = truncate(redactText(strings.TrimSpace(event.Step)), maxFieldBytes)
	event.Status = truncate(redactText(strings.TrimSpace(event.Status)), maxFieldBytes)
	event.ErrorCode = truncate(redactText(strings.TrimSpace(event.ErrorCode)), maxFieldBytes)
	event.ErrorSummary = truncate(redactText(strings.TrimSpace(event.ErrorSummary)), maxMessageBytes)
	event.Pool = truncate(redactText(strings.TrimSpace(event.Pool)), maxFieldBytes)
	event.Agent = truncate(redactText(strings.TrimSpace(event.Agent)), maxFieldBytes)
	event.Resource = truncate(redactText(strings.TrimSpace(event.Resource)), maxFieldBytes)
	event.Provider = truncate(redactText(strings.TrimSpace(event.Provider)), maxFieldBytes)
	event.Request = truncate(redactText(strings.TrimSpace(event.Request)), maxFieldBytes)
	return event
}

func pageForEvents(events []Event, query Query) (Page, error) {
	events = append([]Event(nil), events...)
	sort.Slice(events, func(i, j int) bool { return newer(events[i], events[j]) })
	return pageForOrderedEvents(events, query)
}

func pageForOrderedEvents(events []Event, query Query) (Page, error) {
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
		if query.Provider != "" && event.Provider != query.Provider {
			continue
		}
		if query.Job != "" && event.Job != query.Job {
			continue
		}
		if query.Status != "" && event.Status != query.Status {
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
