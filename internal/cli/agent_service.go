// internal/cli/agent_service.go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/spf13/cobra"
)

// isElevatedFn is isElevated (Task 9) behind a package-level var so tests
// can fake the current process's privilege level.
var isElevatedFn = isElevated

// svcmgrNewManager is svcmgr.NewManager behind a package-level var so
// tests can fake the underlying OS service manager, following the same
// injectable-factory pattern as updateNewUpdater in update.go.
var svcmgrNewManager = svcmgr.NewManager

const agentServiceName = "boxy-agent"

type agentServiceInstallOpts struct {
	userMode  bool
	agentOpts agentServeOpts
}

func newAgentServiceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install, uninstall, start, stop, or check the status of boxy agent as an OS-managed background service",
	}
	cmd.AddCommand(newAgentServiceInstallCommand())
	cmd.AddCommand(newAgentServiceUninstallCommand())
	cmd.AddCommand(newAgentServiceStartCommand())
	cmd.AddCommand(newAgentServiceStopCommand())
	cmd.AddCommand(newAgentServiceStatusCommand())
	return cmd
}

func newAgentServiceInstallCommand() *cobra.Command {
	var opts agentServiceInstallOpts

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install boxy agent as a background service (real service by default; --user for an unprivileged fallback)",
		Long: `Installs boxy agent as an OS-managed background service so it starts
automatically and survives logout/reboot.

By default this registers a real service (Windows Service via SCM, Linux
systemd system unit) and requires an elevated process (Administrator /
root). Pass --user to install the unprivileged fallback instead (Windows
Task Scheduler at-logon task, Linux systemd user unit) — note this starts
at user logon, not at machine boot before any login.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceInstall(cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.userMode, "user", false, "install the unprivileged fallback (no admin/root required) instead of a real service")
	cmd.Flags().StringVar(&opts.agentOpts.server, "server", "", "boxy server gRPC address (host:port), required")
	cmd.Flags().StringSliceVar(&opts.agentOpts.providers, "providers", nil, "provider types this agent hosts (e.g. docker,hyperv), required")
	cmd.Flags().StringVar(&opts.agentOpts.token, "token", "", "single-use registration token (first connection only)")
	cmd.Flags().StringVar(&opts.agentOpts.name, "name", "", "human-readable agent name (default: hostname)")
	cmd.Flags().StringVar(&opts.agentOpts.caCert, "ca-cert", "", "path to the server's CA certificate, required for the first (token) connection unless --insecure")
	cmd.Flags().StringVar(&opts.agentOpts.dataDir, "data-dir", "", "directory for the agent's issued credentials (default .boxy-agent in cwd)")
	cmd.Flags().BoolVar(&opts.agentOpts.insecure, "insecure", false, "connect without TLS (local development only)")
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("providers")
	return cmd
}

func runAgentServiceInstall(cmd *cobra.Command, opts agentServiceInstallOpts) error {
	if !opts.userMode {
		elevated, err := isElevatedFn()
		if err != nil {
			return fmt.Errorf("check process privilege: %w", err)
		}
		if !elevated {
			return fmt.Errorf("installing a real boxy-agent service requires an elevated process (run as Administrator, or as root/sudo) — pass --user to install the unprivileged fallback instead")
		}
	}

	dataDir := opts.agentOpts.dataDir
	if dataDir == "" {
		wd, err := effectiveWD()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		dataDir = filepath.Join(wd, ".boxy-agent")
	}
	absDataDir, err := resolveAbs(dataDir)
	if err != nil {
		return err
	}
	absCACert, err := resolveAbs(opts.agentOpts.caCert)
	if err != nil {
		return err
	}
	logFile := filepath.Join(absDataDir, "service.log")

	cfg := agentServiceConfig{
		Server:    opts.agentOpts.server,
		Providers: stringsOf(opts.agentOpts.providers),
		Token:     opts.agentOpts.token,
		Name:      opts.agentOpts.name,
		CACert:    absCACert,
		DataDir:   absDataDir,
		Insecure:  opts.agentOpts.insecure,
		LogFile:   logFile,
	}
	cfgPath := filepath.Join(absDataDir, "service.yaml")
	if err := saveAgentServiceConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("write service config: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate boxy executable: %w", err)
	}

	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: opts.userMode})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	spec := svcmgr.Spec{
		Name:        agentServiceName,
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent — dials a boxy server and executes provider operations",
		ExecPath:    exePath,
		// --log-file must be a literal arg, not just recorded in the
		// service config: the root command's --log-file is a persistent
		// flag read directly off this process's cmdline (root.go's
		// PersistentPreRunE), so a service invocation with only
		// --service-config on the args list never picks up cfg.LogFile —
		// the service's logs would otherwise go nowhere useful (no
		// console for Windows SCM/Task Scheduler to catch stray output).
		Args: []string{"agent", "serve", "--service-config", cfgPath, "--log-file", logFile},
	}
	if err := mgr.Install(spec); err != nil {
		return fmt.Errorf("install %s service: %w", agentServiceName, err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ boxy-agent installed and started (config: %s, log: %s)\n", cfgPath, logFile)
	return nil
}

// stringsOf normalizes a cobra StringSlice flag value into a plain
// []string with no nil-vs-empty ambiguity for YAML round-tripping.
func stringsOf(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := make([]string, len(ss))
	copy(out, ss)
	return out
}

func newAgentServiceUninstallCommand() *cobra.Command {
	var purge bool
	var dataDir string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedDataDir := dataDir
			if resolvedDataDir == "" {
				wd, err := effectiveWD()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
				resolvedDataDir = filepath.Join(wd, ".boxy-agent")
			}
			return runAgentServiceUninstall(cmd, purge, resolvedDataDir)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the agent's data directory (credentials, state)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "the agent's data directory, to locate it for --purge (default .boxy-agent in cwd)")
	return cmd
}

func runAgentServiceUninstall(cmd *cobra.Command, purge bool, dataDir string) error {
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	if err := mgr.Uninstall(agentServiceName); err != nil {
		return fmt.Errorf("uninstall %s service: %w", agentServiceName, err)
	}
	if purge {
		if err := os.RemoveAll(dataDir); err != nil {
			return fmt.Errorf("remove data directory %q: %w", dataDir, err)
		}
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ boxy-agent service uninstalled\n")
	return nil
}

func newAgentServiceStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Start(agentServiceName); err != nil {
				return fmt.Errorf("start %s service: %w", agentServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-agent service started")
			return nil
		},
	}
}

func newAgentServiceStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Stop(agentServiceName); err != nil {
				return fmt.Errorf("stop %s service: %w", agentServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-agent service stopped")
			return nil
		},
	}
}

func newAgentServiceStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the installed boxy agent service's status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceStatus(cmd)
		},
	}
}

func runAgentServiceStatus(cmd *cobra.Command) error {
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	st, err := mgr.Status(agentServiceName)
	if err != nil {
		return fmt.Errorf("get %s service status: %w", agentServiceName, err)
	}
	if !st.Installed {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "boxy-agent: not installed")
		return nil
	}
	state := "stopped"
	if st.Running {
		state = "running"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "boxy-agent: %s (%s)\n", state, st.Mode)
	return nil
}
