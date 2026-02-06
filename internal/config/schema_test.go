package config

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSchemaJSON_IsValidJSON(t *testing.T) {
	var raw map[string]interface{}
	err := json.Unmarshal(SchemaJSON, &raw)
	require.NoError(t, err, "SchemaJSON must be valid JSON")
}

func TestSchemaJSON_HasExpectedStructure(t *testing.T) {
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(SchemaJSON, &raw))

	expectedKeys := []string{"$schema", "type", "properties", "$defs"}
	for _, key := range expectedKeys {
		assert.Contains(t, raw, key, "schema should contain top-level key %q", key)
	}

	assert.Equal(t, "https://json-schema.org/draft/2020-12/schema", raw["$schema"])
	assert.Equal(t, "object", raw["type"])
}

func TestSchemaJSON_TopLevelProperties(t *testing.T) {
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(SchemaJSON, &raw))

	props, ok := raw["properties"].(map[string]interface{})
	require.True(t, ok, "properties should be an object")

	expectedProps := []string{"pools", "agents", "storage", "logging", "api"}
	for _, prop := range expectedProps {
		assert.Contains(t, props, prop, "properties should contain %q", prop)
	}
}

func TestSchemaJSON_Defs(t *testing.T) {
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(SchemaJSON, &raw))

	defs, ok := raw["$defs"].(map[string]interface{})
	require.True(t, ok, "$defs should be an object")

	expectedDefs := []string{
		"PoolConfig", "AgentConfig", "StorageConfig",
		"LoggingConfig", "APIConfig", "HookConfig",
		"Hook", "TimeoutConfig",
	}
	for _, def := range expectedDefs {
		assert.Contains(t, defs, def, "$defs should contain %q", def)
	}
}

func TestSchemaJSON_ResolvesSuccessfully(t *testing.T) {
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(SchemaJSON, &schema))

	_, err := schema.Resolve(nil)
	require.NoError(t, err, "schema should resolve without errors")
}

func TestSchemaJSON_ValidatesDefaultConfig(t *testing.T) {
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(SchemaJSON, &schema))

	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)

	// Parse the default config YAML into a map for validation
	defaultCfg := `storage:
  type: sqlite
  path: ./boxy.db
logging:
  level: info
  format: text
pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 3
    max_total: 10
    cpus: 2
    memory_mb: 512
    health_check_interval: 30s
`

	var data map[string]interface{}
	require.NoError(t, yaml.Unmarshal([]byte(defaultCfg), &data))

	err = resolved.Validate(data)
	assert.NoError(t, err, "default config should pass schema validation")
}

func TestSchemaJSON_RejectsInvalidConfig(t *testing.T) {
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(SchemaJSON, &schema))

	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)

	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "unknown top-level property",
			yaml: `bogus_key: true`,
		},
		{
			name: "invalid log level",
			yaml: `logging:
  level: verbose`,
		},
		{
			name: "invalid storage type",
			yaml: `storage:
  type: mysql`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data map[string]interface{}
			require.NoError(t, yaml.Unmarshal([]byte(tt.yaml), &data))

			err := resolved.Validate(data)
			assert.Error(t, err, "invalid config should fail schema validation")
		})
	}
}

func TestSchemaFileName(t *testing.T) {
	assert.Equal(t, ".boxy-schema.json", SchemaFileName)
}
