package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"
)

// ExportSchemaVersion identifies the stable JSON contract written by
// WriteExport.
const ExportSchemaVersion = 1

// ComponentSpec describes one producer whose diagnostics are included in an
// export. Components are declared by the implementation that owns them, so a
// consumer can explain where an event came from without knowing Boxy's
// internal package layout.
type ComponentSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Export is the portable, sanitized diagnostics archive.
type Export struct {
	SchemaVersion int             `json:"schema_version"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Sanitized     bool            `json:"sanitized"`
	Components    []ComponentSpec `json:"components"`
	Events        []Event         `json:"events"`
}

// ExportOptions controls metadata for BuildExport. Event limits are enforced
// by the public package and cannot be disabled by a caller.
type ExportOptions struct {
	GeneratedAt time.Time
	Components  []ComponentSpec
}

// BuildExport makes a bounded export and sanitizes every event. Sanitization
// uses stable placeholders for repeated values within this export, preserving
// correlation without exposing machine or user identity.
func BuildExport(events []Event, options ExportOptions) (Export, error) {
	if len(events) > HardMaxLimit {
		return Export{}, fmt.Errorf("diagnostics export contains %d events; maximum is %d", len(events), HardMaxLimit)
	}

	components := make([]ComponentSpec, 0, len(options.Components)+len(events))
	seen := make(map[string]struct{}, len(options.Components)+len(events))
	for _, component := range options.Components {
		component.Name = strings.TrimSpace(component.Name)
		if component.Name == "" {
			return Export{}, errors.New("diagnostics export component name must not be empty")
		}
		if _, ok := seen[component.Name]; ok {
			return Export{}, fmt.Errorf("diagnostics export component %q is declared more than once", component.Name)
		}
		seen[component.Name] = struct{}{}
		components = append(components, component)
	}
	for _, event := range events {
		name := strings.TrimSpace(event.Component)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		components = append(components, ComponentSpec{Name: name})
	}

	sanitizer := NewSanitizer()
	safeEvents := make([]Event, len(events))
	for i, event := range events {
		safeEvents[i] = sanitizer.Event(event)
	}
	generatedAt := options.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	} else {
		generatedAt = generatedAt.UTC()
	}
	return Export{
		SchemaVersion: ExportSchemaVersion,
		GeneratedAt:   generatedAt,
		Sanitized:     true,
		Components:    components,
		Events:        safeEvents,
	}, nil
}

// WriteExport writes the stable, human-readable JSON form of an Export.
func WriteExport(w io.Writer, archive Export) error {
	if w == nil {
		return errors.New("diagnostics export writer is nil")
	}
	if archive.SchemaVersion != ExportSchemaVersion || !archive.Sanitized {
		return errors.New("diagnostics export must be built by BuildExport")
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(archive)
}

// Sanitizer is the public sanitization contract used by both exports and log
// shippers. It is intentionally stateful so repeated values receive the same
// placeholder during one incident report.
type Sanitizer struct {
	values map[string]string
	next   map[string]int
}

// NewSanitizer returns a sanitizer with an empty per-report identity map.
func NewSanitizer() *Sanitizer {
	return &Sanitizer{values: make(map[string]string), next: make(map[string]int)}
}

// Text removes credentials and anonymizes host, network, and user identity
// embedded in diagnostic text.
func (s *Sanitizer) Text(value string) string {
	if s == nil {
		s = NewSanitizer()
	}
	value = exportSignedURL.ReplaceAllString(value, "[SIGNED-URL-REDACTED]")
	value = RedactText(value)
	value = exportBearer.ReplaceAllString(value, `${1}[REDACTED]`)
	value = exportCredentialAssignment.ReplaceAllStringFunc(value, func(match string) string {
		parts := exportCredentialAssignment.FindStringSubmatch(match)
		return parts[1] + "[REDACTED]"
	})
	value = exportUserField.ReplaceAllStringFunc(value, func(match string) string {
		parts := exportUserField.FindStringSubmatch(match)
		return parts[1] + s.placeholder("USER", firstValue(parts[2:]))
	})
	value = exportHostField.ReplaceAllStringFunc(value, func(match string) string {
		parts := exportHostField.FindStringSubmatch(match)
		return parts[1] + s.placeholder("HOST", parts[2])
	})
	value = exportHomePath.ReplaceAllStringFunc(value, func(match string) string {
		parts := exportHomePath.FindStringSubmatch(match)
		return parts[1] + s.placeholder("USER", parts[2])
	})
	value = exportURLHost.ReplaceAllStringFunc(value, func(match string) string {
		parts := exportURLHost.FindStringSubmatch(match)
		return parts[1] + s.placeholder("HOST", parts[2]) + parts[3]
	})
	value = exportIPv4.ReplaceAllStringFunc(value, func(match string) string {
		if net.ParseIP(match) == nil {
			return match
		}
		return s.placeholder("IP", match)
	})
	return exportHostname.ReplaceAllStringFunc(value, func(match string) string {
		return s.placeholder("HOST", match)
	})
}

// Event returns a sanitized copy of event.
func (s *Sanitizer) Event(event Event) Event {
	if s == nil {
		s = NewSanitizer()
	}
	event.ID = s.identifier("EVENT", event.ID)
	event.Message = s.Text(event.Message)
	event.ErrorSummary = s.Text(event.ErrorSummary)
	event.Pool = s.identifier("POOL", event.Pool)
	event.Agent = s.identifier("AGENT", event.Agent)
	event.Resource = s.identifier("RESOURCE", event.Resource)
	event.Request = s.identifier("REQUEST", event.Request)
	return event
}

func (s *Sanitizer) identifier(kind, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return s.placeholder(kind, value)
}

func (s *Sanitizer) placeholder(kind, value string) string {
	key := kind + "\x00" + value
	if existing, ok := s.values[key]; ok {
		return existing
	}
	s.next[kind]++
	placeholder := fmt.Sprintf("[%s-%d]", kind, s.next[kind])
	s.values[key] = placeholder
	return placeholder
}

func firstValue(values []string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}

var (
	exportSignedURL            = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]*(?:x-amz-signature|x-amz-credential|signature=)[^\s"'<>]*`)
	exportBearer               = regexp.MustCompile(`(?i)\b(bearer\s+)[^\s,;]+`)
	exportCredentialAssignment = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|token|api[_-]?key|authorization|credential|private[_-]?key)\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	exportUserField            = regexp.MustCompile(`(?i)(\b(?:username|user|principal|subject)\s*[:=]\s*)(?:"([^"]*)"|'([^']*)'|([^\s,;]+))`)
	exportHostField            = regexp.MustCompile(`(?i)(\b(?:hostname|host|server|node|machine)\s*[:=]\s*)([A-Za-z0-9][A-Za-z0-9._-]*)`)
	exportHomePath             = regexp.MustCompile(`(?i)(\b(?:[A-Z]:\\Users\\|/home/))([^\\/\s]+)`)
	exportURLHost              = regexp.MustCompile(`(?i)(\bhttps?://)([^/\s:]+)(:\d+)?`)
	exportIPv4                 = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	exportHostname             = regexp.MustCompile(`\b[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+\b`)
)
