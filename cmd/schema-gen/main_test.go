package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTopLevelSchemaIncludesProviderTypeRefs(t *testing.T) {
	schema, err := buildTopLevelSchema()
	if err != nil {
		t.Fatalf("buildTopLevelSchema: %v", err)
	}
	if schema["$schema"] == "" || schema["type"] != "object" {
		t.Fatalf("schema = %+v, want top-level object schema", schema)
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", schema["properties"])
	}
	providers, ok := properties["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers = %#v, want map", properties["providers"])
	}
	item, ok := providers["items"].(map[string]any)
	if !ok {
		t.Fatalf("provider item = %#v, want map", providers["items"])
	}
	itemProperties, ok := item["properties"].(map[string]any)
	if !ok {
		t.Fatalf("item properties = %#v, want map", item["properties"])
	}
	typeSchema, ok := itemProperties["type"].(map[string]any)
	if !ok {
		t.Fatalf("type schema = %#v, want map", itemProperties["type"])
	}
	enum, ok := typeSchema["enum"].([]string)
	if !ok {
		t.Fatalf("type enum = %#v, want []string", typeSchema["enum"])
	}
	if len(enum) == 0 {
		t.Fatal("provider type enum is empty")
	}
	allOf, ok := item["allOf"].([]any)
	if !ok {
		t.Fatalf("allOf = %#v, want []any", item["allOf"])
	}
	if len(allOf) != len(enum) {
		t.Fatalf("allOf len = %d, want enum len %d", len(allOf), len(enum))
	}
}

// TestBuildTopLevelSchemaPoolTypeNotRequired guards a real bug caught in
// review: config.ResolvePoolExpectedType treats an omitted pool "type" the
// same as an explicit empty string (both resolve to a container pool), so
// the schema must not require "type" on pool entries — only "name" — or it
// would reject otherwise-valid configs that omit "type" (the schema being
// stricter than the runtime it's meant to describe).
func TestBuildTopLevelSchemaPoolTypeNotRequired(t *testing.T) {
	schema, err := buildTopLevelSchema()
	if err != nil {
		t.Fatalf("buildTopLevelSchema: %v", err)
	}
	properties := schema["properties"].(map[string]any)
	pools, ok := properties["pools"].(map[string]any)
	if !ok {
		t.Fatalf("pools = %#v, want map", properties["pools"])
	}
	item, ok := pools["items"].(map[string]any)
	if !ok {
		t.Fatalf("pool item = %#v, want map", pools["items"])
	}
	required, ok := item["required"].([]string)
	if !ok {
		t.Fatalf("pool item required = %#v, want []string", item["required"])
	}
	for _, r := range required {
		if r == "type" {
			t.Fatal(`pool item requires "type", but an omitted type is valid config (resolves to "container") — required must be ["name"] only`)
		}
	}
	if len(required) != 1 || required[0] != "name" {
		t.Fatalf("pool item required = %v, want [\"name\"]", required)
	}
}

// TestBuildTopLevelSchemaMatchesCommittedFile is a drift guard: nothing
// else in this repo fails if internal/config/schema/boxy.schema.json goes
// stale relative to what buildTopLevelSchema() actually produces — this is
// literally how issue #136 arose (server.grpc_listen/
// agent_heartbeat_interval were added to ServerSpec in v0.1.28 with
// nothing catching that the committed schema wasn't updated). If this test
// fails, run `go generate ./internal/config/schema/...` and commit the
// result.
func TestBuildTopLevelSchemaMatchesCommittedFile(t *testing.T) {
	schema, err := buildTopLevelSchema()
	if err != nil {
		t.Fatalf("buildTopLevelSchema: %v", err)
	}
	got, err := renderSchema(schema)
	if err != nil {
		t.Fatalf("renderSchema: %v", err)
	}

	committedPath := filepath.Join("..", "..", "internal", "config", "schema", "boxy.schema.json")
	want, err := os.ReadFile(committedPath)
	if err != nil {
		t.Fatalf("read committed schema %q: %v", committedPath, err)
	}

	if string(got) != string(want) {
		t.Fatalf("buildTopLevelSchema() output does not match committed %s — run `go generate ./internal/config/schema/...` and commit the result", committedPath)
	}
}

func TestMainWritesSchemaFile(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "schemas", "boxy.schema.json")
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"schema-gen", "-out", outPath}

	main()

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"Boxy Configuration"`) || !strings.HasSuffix(body, "\n") {
		t.Fatalf("schema body = %q, want generated formatted schema", body)
	}
}
