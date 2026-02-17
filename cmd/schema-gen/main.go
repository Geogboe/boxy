package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/builtins"
)

func main() {
	var outPath string
	flag.StringVar(&outPath, "out", "", "output file path (required)")
	flag.Parse()

	if outPath == "" {
		_, _ = fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}

	schema, err := buildTopLevelSchema()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "marshal schema: "+err.Error())
		os.Exit(1)
	}
	b = append(b, '\n')

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mkdir: "+err.Error())
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "write: "+err.Error())
		os.Exit(1)
	}
}

func buildTopLevelSchema() (map[string]any, error) {
	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return nil, fmt.Errorf("register builtins: %w", err)
	}
	ts := reg.Types()
	types := make([]string, 0, len(ts))
	for _, t := range ts {
		types = append(types, string(t))
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("no builtin provider types")
	}

	providerItem := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "type"},
		"properties": map[string]any{
			"name":   map[string]any{"type": "string", "minLength": 1},
			"type":   map[string]any{"type": "string", "enum": types},
			"labels": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"config": map[string]any{"type": "object"},
		},
	}

	allOf := make([]any, 0, len(types))
	for _, t := range types {
		allOf = append(allOf, map[string]any{
			"if": map[string]any{
				"properties": map[string]any{
					"type": map[string]any{"const": t},
				},
			},
			"then": map[string]any{
				"properties": map[string]any{
					"config": map[string]any{
						"$ref": fmt.Sprintf("providers/%s.config.schema.json", t),
					},
				},
			},
		})
	}
	providerItem["allOf"] = allOf

	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "boxy://schemas/boxy.schema.json",
		"title":   "Boxy Configuration",
		"type":    "object",
		"properties": map[string]any{
			"providers": map[string]any{
				"type":  "array",
				"items": providerItem,
			},
		},
	}, nil
}
