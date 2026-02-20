// Command devfactory is a CLI for the devfactory reference provider.
// It lets you exercise the full CRUD lifecycle against the JSON-backed
// driver without running a boxy server or agent.
//
// Usage:
//
//	devfactory create [--label key=value ...]
//	devfactory list
//	devfactory read <id>
//	devfactory exec <id> -- <command> [args...]
//	devfactory set-state <id> <state>
//	devfactory delete <id>
//
// State is persisted to devfactory.json in the data directory (default: .devfactory/).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Geogboe/boxy/v2/pkg/providersdk/providers/devfactory"
)

const defaultDataDir = ".devfactory"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	dataDir := envOrDefault("DEVFACTORY_DATA_DIR", defaultDataDir)
	latency := parseDuration(os.Getenv("DEVFACTORY_LATENCY"))
	profile := devfactory.Profile(envOrDefault("DEVFACTORY_PROFILE", "container"))

	// Parse global flags.
	args := os.Args[1:]
	for len(args) >= 2 {
		switch args[0] {
		case "--data-dir":
			dataDir = args[1]
			args = args[2:]
		case "--profile":
			profile = devfactory.Profile(args[1])
			args = args[2:]
		default:
			goto done
		}
	}
done:

	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	// Ensure data directory exists.
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		fatal("creating data dir: %v", err)
	}

	d := devfactory.New(&devfactory.Config{
		DataDir: dataDir,
		Profile: profile,
		Latency: latency,
	})

	ctx := context.Background()
	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "create":
		doCreate(ctx, d, rest)
	case "list", "ls":
		doList(ctx, d)
	case "read", "get":
		doRead(ctx, d, rest)
	case "exec":
		doExec(ctx, d, rest)
	case "set-state":
		doSetState(ctx, d, rest)
	case "delete", "rm":
		doDelete(ctx, d, rest)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func doCreate(ctx context.Context, d *devfactory.Driver, args []string) {
	labels := parseLabels(args)

	cfg := &devfactory.Config{Labels: labels}
	_ = cfg // labels are set on the driver config, not per-create

	// For per-resource labels, we pass them as the create config.
	res, err := d.Create(ctx, nil)
	if err != nil {
		fatal("create: %v", err)
	}

	printJSON(map[string]any{
		"id":              res.ID,
		"connection_info": res.ConnectionInfo,
		"metadata":        res.Metadata,
	})
}

func doList(ctx context.Context, d *devfactory.Driver) {
	// Read the store file directly for a full listing.
	data, err := os.ReadFile(d.DataDir() + "/devfactory.json")
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("[]")
			return
		}
		fatal("reading store: %v", err)
	}

	var store struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		fatal("parsing store: %v", err)
	}

	resources := make([]json.RawMessage, 0, len(store.Resources))
	for _, r := range store.Resources {
		resources = append(resources, r)
	}

	printJSON(resources)
}

func doRead(ctx context.Context, d *devfactory.Driver, args []string) {
	if len(args) != 1 {
		fatal("usage: devfactory read <id>")
	}

	status, err := d.Read(ctx, args[0])
	if err != nil {
		fatal("read: %v", err)
	}

	printJSON(status)
}

func doExec(ctx context.Context, d *devfactory.Driver, args []string) {
	// Parse: exec <id> -- <command...>
	if len(args) < 1 {
		fatal("usage: devfactory exec <id> -- <command> [args...]")
	}

	id := args[0]
	cmdArgs := args[1:]

	// Strip leading "--" if present.
	if len(cmdArgs) > 0 && cmdArgs[0] == "--" {
		cmdArgs = cmdArgs[1:]
	}
	if len(cmdArgs) == 0 {
		fatal("usage: devfactory exec <id> -- <command> [args...]")
	}

	result, err := d.Update(ctx, id, &devfactory.ExecOp{Command: cmdArgs})
	if err != nil {
		fatal("exec: %v", err)
	}

	printJSON(result)
}

func doSetState(ctx context.Context, d *devfactory.Driver, args []string) {
	if len(args) != 2 {
		fatal("usage: devfactory set-state <id> <state>")
	}

	result, err := d.Update(ctx, args[0], &devfactory.SetStateOp{State: args[1]})
	if err != nil {
		fatal("set-state: %v", err)
	}

	printJSON(result)
}

func doDelete(ctx context.Context, d *devfactory.Driver, args []string) {
	if len(args) != 1 {
		fatal("usage: devfactory delete <id>")
	}

	if err := d.Delete(ctx, args[0]); err != nil {
		fatal("delete: %v", err)
	}
	fmt.Printf("deleted %s\n", args[0])
}

// --- helpers ---

func usage() {
	fmt.Fprintf(os.Stderr, `devfactory — CLI for the devfactory reference provider

Usage:
  devfactory [--data-dir <path>] [--profile <type>] <command> [args...]

Commands:
  create [--label key=value ...]   Create a new resource
  list                             List all resources
  read <id>                        Read resource state
  exec <id> -- <cmd> [args...]     Simulate command execution
  set-state <id> <state>           Change resource state
  delete <id>                      Delete a resource

Profiles:
  container   Simulates containers (host:port, instant provisioning)
  vm          Simulates VMs (SSH connection info, 2s default latency)
  share       Simulates network shares (UNC path, mount path, credentials)

Environment:
  DEVFACTORY_DATA_DIR   Data directory (default: .devfactory/)
  DEVFACTORY_LATENCY    Provisioning latency, e.g. "500ms" (default: profile)
  DEVFACTORY_PROFILE    Resource profile (default: container)

State is persisted to devfactory.json in the data directory.
`)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatal("encoding JSON: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fatal("invalid duration %q: %v", s, err)
	}
	return d
}

func parseLabels(args []string) map[string]string {
	labels := make(map[string]string)
	for i := 0; i < len(args); i++ {
		if args[i] == "--label" || args[i] == "-l" {
			i++
			if i >= len(args) {
				fatal("--label requires a key=value argument")
			}
			parts := strings.SplitN(args[i], "=", 2)
			if len(parts) != 2 {
				fatal("invalid label format %q, expected key=value", args[i])
			}
			labels[parts[0]] = parts[1]
		}
	}
	return labels
}
