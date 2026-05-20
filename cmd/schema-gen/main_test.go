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
