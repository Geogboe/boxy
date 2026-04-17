package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/pterm/pterm"
	"golang.org/x/term"
)

const sandboxPollInterval = time.Second

type createSandboxBody struct {
	Name     string                  `json:"name"`
	Policies model.SandboxPolicies   `json:"policies,omitempty"`
	Requests []model.ResourceRequest `json:"requests"`
}

func sandboxCreate(ctx context.Context, opts sandboxCreateOpts) error {
	pterm.Println()

	doneSpec := step("Loading sandbox spec")
	spec, err := loadSandboxSpec(opts.file)
	if err != nil {
		return err
	}
	doneSpec(fmt.Sprintf("%s  (%d resource group(s))", spec.Name, len(spec.Resources)))

	client := defaultAPIClient()
	base := apiBaseURL(opts.server)

	donePools := step("Loading pool catalog")
	pools, err := fetchJSON[[]model.Pool](ctx, client, base+"/api/v1/pools")
	if err != nil {
		return fmt.Errorf("load pool catalog: %w", err)
	}
	donePools(fmt.Sprintf("%d pool(s)", len(pools)))

	requests, err := compileSandboxRequests(spec, pools)
	if err != nil {
		return err
	}

	doneCreate := step(fmt.Sprintf("Creating sandbox %q", spec.Name))
	sb, err := postJSON[createSandboxBody, model.Sandbox](ctx, client, base+"/api/v1/sandboxes", createSandboxBody{
		Name:     spec.Name,
		Policies: model.SandboxPolicies{},
		Requests: requests,
	})
	if err != nil {
		return fmt.Errorf("create sandbox %q: %w", spec.Name, err)
	}
	doneCreate(fmt.Sprintf("%s  ·  %s", sb.ID, sb.Status))

	if opts.noWait {
		printAcceptedSandbox(sb)
		printSandboxCommands(sb.ID)
		return nil
	}

	sb, err = waitForSandboxReady(ctx, client, base, sb)
	if err != nil {
		printSandboxCommands(sb.ID)
		return err
	}

	resources, err := hydrateSandboxResources(ctx, client, base, sb)
	if err != nil {
		return err
	}

	printConnectionInfo(sb, resources)

	if !opts.noEnvFile && term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // Fd() fits in int on all supported platforms
		if err := promptEnvFile(sb, resources); err != nil {
			return err
		}
	}

	printSandboxCommands(sb.ID)
	pterm.Println()
	return nil
}

func loadSandboxSpec(path string) (boxyconfig.SandboxSpec, error) {
	spec, err := boxyconfig.LoadSandboxFile(path)
	if err != nil {
		return boxyconfig.SandboxSpec{}, err
	}
	if strings.TrimSpace(spec.Name) == "" {
		return boxyconfig.SandboxSpec{}, fmt.Errorf("sandbox spec name is required")
	}
	if len(spec.Resources) == 0 {
		return boxyconfig.SandboxSpec{}, fmt.Errorf("sandbox spec resources is required")
	}
	for i := range spec.Resources {
		res := spec.Resources[i]
		if strings.TrimSpace(res.Pool) == "" {
			return boxyconfig.SandboxSpec{}, fmt.Errorf("resources[%d].pool is required", i)
		}
		if res.Count <= 0 {
			return boxyconfig.SandboxSpec{}, fmt.Errorf("resources[%d].count must be > 0", i)
		}
	}
	return spec, nil
}

func compileSandboxRequests(spec boxyconfig.SandboxSpec, pools []model.Pool) ([]model.ResourceRequest, error) {
	poolByName := make(map[model.PoolName]model.Pool, len(pools))
	for _, pool := range pools {
		poolByName[pool.Name] = pool
	}

	requests := make([]model.ResourceRequest, 0, len(spec.Resources))
	for i := range spec.Resources {
		res := spec.Resources[i]
		poolName := model.PoolName(strings.TrimSpace(res.Pool))
		pool, ok := poolByName[poolName]
		if !ok {
			return nil, fmt.Errorf("resources[%d].pool %q not found on server", i, res.Pool)
		}
		if pool.Inventory.ExpectedType == "" || pool.Inventory.ExpectedType == model.ResourceTypeUnknown {
			return nil, fmt.Errorf("pool %q is missing expected resource type", pool.Name)
		}
		if strings.TrimSpace(string(pool.Inventory.ExpectedProfile)) == "" {
			return nil, fmt.Errorf("pool %q is missing expected resource profile", pool.Name)
		}

		requests = append(requests, model.ResourceRequest{
			Type:    pool.Inventory.ExpectedType,
			Profile: pool.Inventory.ExpectedProfile,
			Count:   res.Count,
		})
	}

	return requests, nil
}

func waitForSandboxReady(ctx context.Context, client *http.Client, base string, initial model.Sandbox) (model.Sandbox, error) {
	var (
		spin       *pterm.SpinnerPrinter
		useSpinner = useSpinnerOutput()
	)
	if useSpinner {
		started, _ := boxySpinner.Start(fmt.Sprintf("Waiting for sandbox %q", initial.Name))
		spin = started
		defer func() {
			if spin.IsActive {
				_ = spin.Stop()
			}
		}()
	}

	sb := initial
	ticker := time.NewTicker(sandboxPollInterval)
	defer ticker.Stop()

	for {
		switch sb.Status {
		case model.SandboxStatusReady:
			msg := fmt.Sprintf("Waiting for sandbox %q  %s", sb.Name, pterm.FgDarkGray.Sprintf("%s  ·  %d resource(s)", sb.ID, len(sb.Resources)))
			if useSpinner {
				spin.Success(msg)
			} else {
				boxySuccessPrinter.Println(msg)
			}
			return sb, nil
		case model.SandboxStatusFailed:
			msg := strings.TrimSpace(sb.Error)
			if msg == "" {
				msg = "sandbox request failed"
			}
			if useSpinner {
				spin.Fail(msg)
			} else {
				boxyFailPrinter.Println(msg)
			}
			return sb, fmt.Errorf("sandbox %q failed: %s", sb.ID, msg)
		}

		select {
		case <-ctx.Done():
			if useSpinner {
				spin.Fail("interrupted")
			} else {
				boxyFailPrinter.Println("interrupted")
			}
			return sb, fmt.Errorf("sandbox %q created but wait was interrupted: %w", sb.ID, ctx.Err())
		case <-ticker.C:
		}

		next, err := fetchJSON[model.Sandbox](ctx, client, base+"/api/v1/sandboxes/"+string(sb.ID))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if useSpinner {
					spin.Fail("interrupted")
				} else {
					boxyFailPrinter.Println("interrupted")
				}
				return sb, fmt.Errorf("sandbox %q created but wait was interrupted: %w", sb.ID, err)
			}
			if useSpinner {
				spin.Fail(err.Error())
			} else {
				boxyFailPrinter.Println(err.Error())
			}
			return sb, fmt.Errorf("wait for sandbox %q: %w", sb.ID, err)
		}
		sb = next
	}
}

func hydrateSandboxResources(ctx context.Context, client *http.Client, base string, sb model.Sandbox) ([]model.Resource, error) {
	resources := make([]model.Resource, 0, len(sb.Resources))
	for _, rid := range sb.Resources {
		res, err := fetchJSON[model.Resource](ctx, client, base+"/api/v1/resources/"+string(rid))
		if err != nil {
			var apiErr *apiError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				continue
			}
			return nil, fmt.Errorf("get resource %q for sandbox %q: %w", rid, sb.ID, err)
		}
		resources = append(resources, res)
	}
	return resources, nil
}

func printAcceptedSandbox(sb model.Sandbox) {
	pterm.Println()
	pterm.Bold.Printfln("  Sandbox accepted")
	pterm.Println()
	pterm.Printfln("    id: %s", sb.ID)
	pterm.Printfln("    name: %s", sb.Name)
	pterm.Printfln("    status: %s", sb.Status)
	pterm.Println()
}

func printSandboxCommands(id model.SandboxID) {
	pterm.FgDarkGray.Printfln("  boxy sandbox get %s", id)
	pterm.FgDarkGray.Printfln("  boxy sandbox delete %s", id)
}

// printConnectionInfo displays sandbox connection info once to the terminal.
func printConnectionInfo(sb model.Sandbox, resources []model.Resource) {
	if len(resources) == 0 {
		return
	}
	lines := buildEnvLines(sb, resources)

	pterm.Println()
	pterm.Bold.Println("  Connection info  (save this — you won't see it again)")
	pterm.Println()
	for _, l := range lines {
		pterm.Printfln("    %s", l)
	}
	pterm.Println()
}

// promptEnvFile asks the user whether to save connection info as an env file.
func promptEnvFile(sb model.Sandbox, resources []model.Resource) error {
	envFileName := fmt.Sprintf(".sandbox-%s.env", sb.Name)
	pterm.Printfln("  [e] Save as %s   [enter] Skip", envFileName)
	pterm.Println()

	key, err := readSingleKey()
	if err != nil {
		return nil // non-fatal if we can't read the key
	}

	if key == 'e' || key == 'E' {
		return writeEnvFile(sb, resources, envFileName)
	}
	return nil
}

// writeEnvFile writes sandbox connection info to .sandbox-<name>.env in cwd.
func writeEnvFile(sb model.Sandbox, resources []model.Resource, filename string) error {
	wd, err := effectiveWD()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	path := filepath.Join(wd, filename)

	lines := buildEnvLines(sb, resources)
	var buf strings.Builder
	fmt.Fprintf(&buf, "# Boxy sandbox: %s (%s)\n", sb.Name, sb.ID)
	buf.WriteString("# Add this file to .gitignore — it may contain credentials.\n")
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(buf.String()), 0600); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}

	pterm.Println()
	pterm.Printfln("  Wrote %s", path)
	pterm.Warning.Println("  Add this file to .gitignore — it may contain credentials.")
	pterm.Println()
	return nil
}

// buildEnvLines builds the list of KEY=VALUE lines for the env file / display.
func buildEnvLines(sb model.Sandbox, resources []model.Resource) []string {
	var lines []string
	lines = append(lines, fmt.Sprintf("SANDBOX_ID=%s", sb.ID))
	lines = append(lines, "")

	seenPools := make(map[string]int) // pool -> count so far
	poolOrder := make([]string, 0)

	for _, res := range resources {
		poolName := string(res.Profile)
		if _, seen := seenPools[poolName]; !seen {
			seenPools[poolName] = 0
			poolOrder = append(poolOrder, poolName)
		}
	}
	// Maintain order: rebuild groups indexed by pool name.
	poolIdx := make(map[string]int, len(poolOrder))
	for i, name := range poolOrder {
		poolIdx[name] = i
	}
	groupSlice := make([][]model.Resource, len(poolOrder))
	for _, res := range resources {
		poolName := string(res.Profile)
		i := poolIdx[poolName]
		groupSlice[i] = append(groupSlice[i], res)
	}

	for pi, poolName := range poolOrder {
		prefix := "SANDBOX_" + envVarSegment(poolName)
		for n, res := range groupSlice[pi] {
			varPrefix := fmt.Sprintf("%s_%d", prefix, n+1)

			// Collect all property keys and sort for deterministic output.
			keys := make([]string, 0, len(res.Properties))
			for k := range res.Properties {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				v := res.Properties[k]
				envKey := varPrefix + "_" + envVarSegment(k)
				lines = append(lines, fmt.Sprintf("%s=%v", envKey, v))
			}
			if pi < len(poolOrder)-1 || n < len(groupSlice[pi])-1 {
				lines = append(lines, "")
			}
		}
	}
	return lines
}

// envVarSegment converts a string to an uppercase env var segment
// (hyphens and dots become underscores).
func envVarSegment(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	return s
}

// readSingleKey reads a single keypress without requiring Enter.
// Returns 0 and nil on non-interactive stdin.
func readSingleKey() (byte, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // Fd() fits in int on all supported platforms
		return 0, nil
	}
	old, err := term.MakeRaw(int(os.Stdin.Fd())) //nolint:gosec // Fd() fits in int on all supported platforms
	if err != nil {
		return 0, nil
	}
	defer term.Restore(int(os.Stdin.Fd()), old) //nolint:errcheck,gosec // Fd() fits in int on all supported platforms

	buf := make([]byte, 1)
	if _, err := os.Stdin.Read(buf); err != nil {
		return 0, nil
	}
	return buf[0], nil
}
