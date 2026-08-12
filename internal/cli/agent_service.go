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

// instanceNameFlagHelp is shared verbatim across every agent/serve service
// subcommand's --instance-name flag so their --help text stays identical.
const instanceNameFlagHelp = "target a named instance installed with --instance-name (default: the unnamed instance)"

type agentServiceInstallOpts struct {
	userMode     bool
	instanceName string
	agentOpts    agentServeOpts
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
at user logon, not at machine boot before any login.

Pass --instance-name to install more than one agent instance on the same
host (e.g. to test two provider configs side by side) — it produces a
distinctly named service (boxy-agent-<name>) and, unless --data-dir is
also given, a distinctly named default data directory
(.boxy-agent-<name>). uninstall/start/stop/status all take the same
--instance-name to target that instance.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceInstall(cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.userMode, "user", false, "install the unprivileged fallback (no admin/root required) instead of a real service")
	cmd.Flags().StringVar(&opts.instanceName, "instance-name", "", "install as a named instance (produces boxy-agent-<name> and .boxy-agent-<name>) instead of the default single instance")
	cmd.Flags().StringVar(&opts.agentOpts.server, "server", "", "boxy server gRPC address (host:port), required")
	cmd.Flags().StringSliceVar(&opts.agentOpts.providers, "providers", nil, "provider types this agent hosts (e.g. docker,hyperv), required")
	cmd.Flags().StringVar(&opts.agentOpts.token, "token", "", "single-use registration token (first connection only)")
	cmd.Flags().StringVar(&opts.agentOpts.name, "name", "", "human-readable agent name (default: hostname)")
	cmd.Flags().StringVar(&opts.agentOpts.caCert, "ca-cert", "", "path to the server's CA certificate, required for the first (token) connection unless --insecure")
	cmd.Flags().StringVar(&opts.agentOpts.dataDir, "data-dir", "", "directory for the agent's issued credentials (default .boxy-agent[-<instance-name>] in cwd)")
	cmd.Flags().BoolVar(&opts.agentOpts.insecure, "insecure", false, "connect without TLS (local development only)")
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("providers")
	return cmd
}

func runAgentServiceInstall(cmd *cobra.Command, opts agentServiceInstallOpts) error {
	if err := validateInstanceName(opts.instanceName); err != nil {
		return err
	}
	svcName := serviceInstanceName(agentServiceName, opts.instanceName)

	if !opts.userMode {
		elevated, err := isElevatedFn()
		if err != nil {
			return fmt.Errorf("check process privilege: %w", err)
		}
		if !elevated {
			return fmt.Errorf("installing a real %s service requires an elevated process (run as Administrator, or as root/sudo) — pass --user to install the unprivileged fallback instead", svcName)
		}
	}

	dataDir := opts.agentOpts.dataDir
	if dataDir == "" {
		wd, err := effectiveWD()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		dataDir = filepath.Join(wd, ".boxy-agent"+instanceDirSuffix(opts.instanceName))
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
		Name:        svcName,
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
		return fmt.Errorf("install %s service: %w", svcName, err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ %s installed and started (config: %s, log: %s)\n", svcName, cfgPath, logFile)
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

type agentServiceUninstallOpts struct {
	purge        bool
	dataDir      string
	instanceName string
	userMode     bool
}

func newAgentServiceUninstallCommand() *cobra.Command {
	var opts agentServiceUninstallOpts
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceUninstall(cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.purge, "purge", false, "also remove the agent's data directory (credentials, state)")
	cmd.Flags().StringVar(&opts.dataDir, "data-dir", "", "the agent's data directory, to locate it for --purge (default .boxy-agent[-<instance-name>] in cwd)")
	cmd.Flags().StringVar(&opts.instanceName, "instance-name", "", instanceNameFlagHelp)
	cmd.Flags().BoolVar(&opts.userMode, "user", false, "target the --user (unprivileged) instance — must match how it was installed")
	return cmd
}

func runAgentServiceUninstall(cmd *cobra.Command, opts agentServiceUninstallOpts) error {
	if err := validateInstanceName(opts.instanceName); err != nil {
		return err
	}
	svcName := serviceInstanceName(agentServiceName, opts.instanceName)

	dataDir := opts.dataDir
	if dataDir == "" {
		wd, err := effectiveWD()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		dataDir = filepath.Join(wd, ".boxy-agent"+instanceDirSuffix(opts.instanceName))
	}

	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: opts.userMode})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	if err := mgr.Uninstall(svcName); err != nil {
		return fmt.Errorf("uninstall %s service: %w", svcName, err)
	}
	if opts.purge {
		if err := purgeServiceDataDir(dataDir); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ %s service uninstalled\n", svcName)
	return nil
}

func newAgentServiceStartCommand() *cobra.Command {
	var instanceName string
	var userMode bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceStart(cmd, instanceName, userMode)
		},
	}
	cmd.Flags().StringVar(&instanceName, "instance-name", "", instanceNameFlagHelp)
	cmd.Flags().BoolVar(&userMode, "user", false, "target the --user (unprivileged) instance — must match how it was installed")
	return cmd
}

func runAgentServiceStart(cmd *cobra.Command, instanceName string, userMode bool) error {
	if err := validateInstanceName(instanceName); err != nil {
		return err
	}
	svcName := serviceInstanceName(agentServiceName, instanceName)
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: userMode})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	if err := mgr.Start(svcName); err != nil {
		return fmt.Errorf("start %s service: %w", svcName, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ %s service started\n", svcName)
	return nil
}

func newAgentServiceStopCommand() *cobra.Command {
	var instanceName string
	var userMode bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceStop(cmd, instanceName, userMode)
		},
	}
	cmd.Flags().StringVar(&instanceName, "instance-name", "", instanceNameFlagHelp)
	cmd.Flags().BoolVar(&userMode, "user", false, "target the --user (unprivileged) instance — must match how it was installed")
	return cmd
}

func runAgentServiceStop(cmd *cobra.Command, instanceName string, userMode bool) error {
	if err := validateInstanceName(instanceName); err != nil {
		return err
	}
	svcName := serviceInstanceName(agentServiceName, instanceName)
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: userMode})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	if err := mgr.Stop(svcName); err != nil {
		return fmt.Errorf("stop %s service: %w", svcName, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ %s service stopped\n", svcName)
	return nil
}

func newAgentServiceStatusCommand() *cobra.Command {
	var instanceName string
	var userMode bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the installed boxy agent service's status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceStatus(cmd, instanceName, userMode)
		},
	}
	cmd.Flags().StringVar(&instanceName, "instance-name", "", instanceNameFlagHelp)
	cmd.Flags().BoolVar(&userMode, "user", false, "target the --user (unprivileged) instance — must match how it was installed")
	return cmd
}

func runAgentServiceStatus(cmd *cobra.Command, instanceName string, userMode bool) error {
	if err := validateInstanceName(instanceName); err != nil {
		return err
	}
	svcName := serviceInstanceName(agentServiceName, instanceName)
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: userMode})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	st, err := mgr.Status(svcName)
	if err != nil {
		return fmt.Errorf("get %s service status: %w", svcName, err)
	}
	if !st.Installed {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: not installed\n", svcName)
		return nil
	}
	state := "stopped"
	if st.Running {
		state = "running"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", svcName, state, st.Mode)
	return nil
}
