package diagnostics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Handler forwards records to the normal slog handler and independently
// stores a safe diagnostics projection. Storage failures are deliberately
// ignored so an observability disk problem cannot break the application.
type Handler struct {
	base   slog.Handler
	store  Store
	attrs  []slog.Attr
	groups []string
}

func NewHandler(base slog.Handler, store Store) *Handler {
	if base == nil {
		base = slog.NewTextHandler(io.Discard, nil)
	}
	return &Handler{base: base, store: store}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	err := h.base.Handle(ctx, record)
	if h.store != nil {
		_ = h.store.Append(ctx, h.event(record))
	}
	return err
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	clone.base = h.base.WithAttrs(attrs)
	return &clone
}

func (h *Handler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	clone.base = h.base.WithGroup(name)
	return &clone
}

func (h *Handler) event(record slog.Record) Event {
	return eventFromSlog(record, h.attrs, h.groups)
}

func eventFromSlog(record slog.Record, attrs []slog.Attr, groups []string) Event {
	values := make(map[string]string)
	add := func(attr slog.Attr) {
		key := strings.ToLower(strings.TrimSpace(attr.Key))
		canonical, ok := safeField(key)
		if !ok || len(groups) != 0 {
			return
		}
		attr.Value = attr.Value.Resolve()
		values[canonical] = truncate(RedactText(fmt.Sprint(attr.Value.Any())), maxFieldBytes)
	}
	for _, attr := range attrs {
		add(attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		add(attr)
		return true
	})
	return Event{
		Timestamp:    record.Time,
		Level:        record.Level.String(),
		Component:    values["component"],
		Message:      RedactText(record.Message),
		Operation:    values["operation"],
		ErrorCode:    values["error_code"],
		ErrorSummary: values["error_summary"],
		Pool:         values["pool"],
		Agent:        values["agent"],
		Resource:     values["resource"],
		Request:      values["request"],
	}
}

func safeField(key string) (string, bool) {
	switch key {
	case "component":
		return "component", true
	case "pool", "pool_name":
		return "pool", true
	case "agent", "agent_id":
		return "agent", true
	case "resource", "resource_id":
		return "resource", true
	case "request", "request_id", "correlation_id":
		return "request", true
	case "operation":
		return "operation", true
	case "error_code":
		return "error_code", true
	case "error_summary":
		return "error_summary", true
	default:
		return "", false
	}
}
