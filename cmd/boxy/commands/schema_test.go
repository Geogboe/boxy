package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/config"
)

func TestSchemaCmd_Stdout(t *testing.T) {
	// Capture stdout by redirecting it
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	cmd := rootCmd
	cmd.SetArgs([]string{"schema"})
	require.NoError(t, cmd.Execute())

	w.Close()
	os.Stdout = old

	buf := make([]byte, len(config.SchemaJSON)+1024)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	// Verify it's valid JSON
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(output), &raw), "stdout output should be valid JSON")
	assert.Contains(t, raw, "$schema")
}

func TestSchemaCmd_OutputFlag(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := rootCmd
	cmd.SetArgs([]string{"schema", "--output", tmpDir})
	require.NoError(t, cmd.Execute())

	// Verify file was written
	schemaPath := filepath.Join(tmpDir, config.SchemaFileName)
	data, err := os.ReadFile(schemaPath)
	require.NoError(t, err, "schema file should exist at %s", schemaPath)

	// Verify it's valid JSON matching the embedded schema
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Contains(t, raw, "$schema")
	assert.Equal(t, config.SchemaJSON, data)
}
