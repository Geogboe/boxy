package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
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

	b, err := renderSchema(schema)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mkdir: "+err.Error())
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, b, 0o600); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "write: "+err.Error())
		os.Exit(1)
	}
}

// renderSchema formats schema the same way it's written to disk. Shared by
// main() and the drift test in main_test.go, so the drift test can't itself
// drift from what main() actually writes.
func renderSchema(schema map[string]any) ([]byte, error) {
	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return append(b, '\n'), nil
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
		// additionalProperties is deliberately left permissive at this top
		// level (not set to false): this schema is also applied to
		// *.sandbox.yaml files (see .devcontainer/devcontainer.json),
		// which have an entirely different top-level shape (name,
		// resources). Locking down unknown keys is done within each of
		// "server" and "pools" instead — see buildServerSchema/
		// buildPoolsSchema — which is where issue #136's actual complaint
		// (server.* fields silently unvalidated) lives.
		"type": "object",
		"properties": map[string]any{
			"providers": map[string]any{
				"type":  "array",
				"items": providerItem,
			},
			"server": buildServerSchema(),
			"pools":  buildPoolsSchema(),
		},
	}, nil
}

// buildServerSchema describes boxy.yaml's top-level "server" object,
// mirroring internal/config.ServerSpec field-for-field (see that struct's
// doc comments for the source of truth these descriptions restate).
func buildServerSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"listen": map[string]any{
				"type":        "string",
				"description": "HTTP API listen address. Empty means the default (:9090).",
			},
			"providers": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Provider instance names to expose via the API/UI (subset of the top-level providers list).",
			},
			"ui": map[string]any{
				"type":        "boolean",
				"description": "Whether the web dashboard is served alongside the API. Default: true.",
			},
			"grpc_listen": map[string]any{
				"type":        "string",
				"description": "Agent-transport gRPC listen address (see docs/adr/0005-remote-agent-transport-and-registration.md). Empty means the default (:9091).",
			},
			"agent_heartbeat_interval": map[string]any{
				"type":        "string",
				"description": "How often connected remote agents send heartbeats, as a Go duration string (e.g. \"15s\"). Empty means the default (15s).",
			},
			"grpc_cert_sans": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Extra DNS names/IPs to include in the agent gRPC server certificate's SANs, on top of the always-included localhost/127.0.0.1/listen-host entries. Needed when remote agents connect through a passthrough route or load balancer using an external DNS name. Equivalent repeatable CLI flag: --grpc-cert-san (fully overrides this value when passed).",
			},
			"secrets": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"description":          "Explicit server-owned secret backend. Required for guest-personalizable pools; there is no automatic fallback.",
				"properties": map[string]any{
					"backend": map[string]any{
						"type":        "string",
						"enum":        []string{"file", "keyring", "dpapi"},
						"description": "Secret backend: file for ACL/mode-protected portable deployments, keyring for local OS keychains, or dpapi for Windows machine-scope protection.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Backend file path for file or dpapi. Relative paths are resolved beside .boxy/state.json.",
					},
					"service": map[string]any{
						"type":        "string",
						"description": "Optional OS keyring service name for the keyring backend.",
					},
				},
			},
		},
	}
}

// buildPoolsSchema describes boxy.yaml's top-level "pools" array,
// mirroring internal/config.PoolSpec/PoolPolicySpec. Some Go-side-only
// constraints (policy/policies mutual exclusivity, max_total: 0 meaning
// "drain") can't be expressed as plain JSON Schema properties and are
// documented in prose here instead, matching how the provider config
// schemas already handle constraints structural JSON Schema can't capture.
func buildPoolsSchema() map[string]any {
	policySchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          `"policy" and "policies" are aliases for the same setting and are mutually exclusive — set only one.`,
		"properties": map[string]any{
			"preheat": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"min_ready": map[string]any{
						"type":        "integer",
						"description": "Minimum number of pre-provisioned ready resources to maintain.",
					},
					"max_total": map[string]any{
						"type":        "integer",
						"description": `Maximum total resources for this pool. 0 is a deliberate, valid value meaning "drain" (stop preheating and let the pool empty), not an omission.`,
					},
				},
			},
			"recycle": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"max_age": map[string]any{
						"type":        "string",
						"description": "Maximum resource age before recycling, as a Go duration string (e.g. \"1h\").",
					},
				},
			},
		},
	}

	poolItem := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		// "type" is deliberately NOT required here, unlike the provider
		// item above: config loading treats an omitted type the same as
		// an explicitly empty string, and ResolvePoolExpectedType maps ""
		// to a container pool (see config.go's ResolvePoolExpectedType and
		// the "enum" below, which includes ""). Requiring the key would
		// make this schema stricter than the runtime actually is and flag
		// otherwise-valid configs as invalid.
		"required": []string{"name"},
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "minLength": 1},
			"type": map[string]any{
				"type": "string",
				// "" is a valid, explicitly-empty config value that
				// resolves to "container" (see
				// config.ResolvePoolExpectedType) — included explicitly
				// rather than relying on omitting the key, since "type: """
				// and omitting "type" entirely are both valid but distinct
				// inputs.
				"enum":        []string{"", "container", "docker", "vm", "share"},
				"description": `Pool kind. "" and "container" and "docker" all resolve to a container pool.`,
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "Optional provider instance name. Some pool types (like \"docker\") may imply a default provider.",
			},
			"config": map[string]any{
				"type":        "object",
				"description": "Provider/pool-type-specific configuration.",
			},
			"agent": map[string]any{
				"type":        "string",
				"description": "Optional agent instance ID this pool is pinned to.",
			},
			"policy":   policySchema,
			"policies": policySchema,
		},
	}

	return map[string]any{
		"type":  "array",
		"items": poolItem,
	}
}
