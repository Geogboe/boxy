package main

import (
	"fmt"
	"os"

	"github.com/Geogboe/boxy/cmd/boxy/commands"
)

// Version information - set via ldflags during build
// Example: go build -ldflags="-X main.Version=1.0.0"
var (
	Version   = "dev"     // Version number
	GitCommit = "unknown" // Git commit hash
	BuildDate = "unknown" // Build date
)

func main() {
	// Set version info for commands to use
	commands.SetVersionInfo(Version, GitCommit, BuildDate)

	if err := commands.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
