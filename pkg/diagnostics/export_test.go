package diagnostics

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildExportSanitizesSensitiveValuesAndPreservesCorrelation(t *testing.T) {
	events := []Event{{
		ID:        "event-1",
		Timestamp: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		Level:     "ERROR",
		Component: "agent",
		Message:   "connect to https://worker.example.test/path?X-Amz-Signature=secret&x=1 from 10.20.30.40 as username=George password=topsecret",
		Agent:     "worker.example.test",
		Resource:  "resource-1",
		Pool:      "production-pool",
	}}

	archive, err := BuildExport(events, ExportOptions{
		GeneratedAt: time.Date(2026, 9, 3, 12, 1, 0, 0, time.UTC),
		Components:  []ComponentSpec{{Name: "agent", Description: "authenticated provider agents"}},
	})
	if err != nil {
		t.Fatalf("BuildExport: %v", err)
	}

	if !archive.Sanitized || archive.SchemaVersion != ExportSchemaVersion {
		t.Fatalf("archive metadata = %+v, want sanitized schema %d", archive, ExportSchemaVersion)
	}
	if len(archive.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(archive.Events))
	}
	message := archive.Events[0].Message
	for _, secret := range []string{"secret", "topsecret", "George", "worker.example.test", "10.20.30.40"} {
		if strings.Contains(message, secret) {
			t.Fatalf("sanitized message contains %q: %s", secret, message)
		}
	}
	if !strings.Contains(message, "[SIGNED-URL-REDACTED]") || !strings.Contains(message, "[USER-1]") {
		t.Fatalf("message = %q, want signed URL and user placeholders", message)
	}
	if archive.Events[0].Agent != "[AGENT-1]" || archive.Events[0].Resource != "[RESOURCE-1]" {
		t.Fatalf("identifiers = agent %q resource %q, want stable placeholders", archive.Events[0].Agent, archive.Events[0].Resource)
	}

	var buf bytes.Buffer
	if err := WriteExport(&buf, archive); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	var decoded Export
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if len(decoded.Events) != 1 || decoded.Events[0].Message != message {
		t.Fatalf("round trip changed sanitized event: %+v", decoded.Events)
	}
}

func TestBuildExportAddsUndeclaredEventComponents(t *testing.T) {
	archive, err := BuildExport([]Event{{Component: "provider", Message: "ok"}}, ExportOptions{
		Components: []ComponentSpec{{Name: "agent"}},
	})
	if err != nil {
		t.Fatalf("BuildExport: %v", err)
	}
	if len(archive.Components) != 2 || archive.Components[1].Name != "provider" {
		t.Fatalf("components = %+v, want declared component followed by provider", archive.Components)
	}
}

func TestBuildExportRejectsMoreThanHardLimit(t *testing.T) {
	events := make([]Event, HardMaxLimit+1)
	if _, err := BuildExport(events, ExportOptions{}); err == nil {
		t.Fatal("BuildExport accepted an unbounded export")
	}
}
