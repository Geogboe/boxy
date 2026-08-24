package cli

import (
	"encoding/json"
	"fmt"
	"os"
)

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}

// validateListFormat rejects a --format value that isn't "", "json", or
// "table" — the shared contract behind every list/status command's
// `--format json|table` flag (default table).
func validateListFormat(format string) error {
	if format != "" && format != "json" && format != "table" {
		return fmt.Errorf("unknown --format %q: want json or table", format)
	}
	return nil
}
