package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/pterm/pterm"
)

// printSandboxCreated writes a human-friendly sandbox creation summary to stderr.
func printSandboxCreated(sb model.Sandbox) {
	pterm.Println()
	pterm.Bold.Println("  Sandbox created")
	pterm.Println()
	pterm.Printfln("    ID:        %s", sb.ID)
	pterm.Printfln("    Name:      %s", sb.Name)
	pterm.Printfln("    Resources: %d", len(sb.Resources))
	pterm.Println()
	pterm.Bold.Println("  Next steps")
	pterm.Println()
	pterm.Printfln("    boxy sandbox get %s", sb.ID)
	pterm.Printfln("    boxy sandbox delete %s", sb.ID)
	pterm.Println()
}

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}
