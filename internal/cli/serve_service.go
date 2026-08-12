// internal/cli/serve_service.go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/spf13/cobra"
)

const serveServiceName = "boxy-serve"

type serveServiceInstallOpts struct {
	userMode  bool
	serveOpts serveOpts
	// boxyDir overrides where service.yaml/service.log are written;
	// empty means the real default (resolved the same way serveStatePath
	// resolves .boxy/, so the service config sits next to state.json).
	boxyDir string
}

func newServeServiceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install, uninstall, start, stop, or check the status of boxy serve as an OS-managed background service",
	}
	cmd.AddCommand(newServeServiceInstallCommand())
	cmd.AddCommand(newServeServiceUninstallCommand())
	cmd.AddCommand(newServeServiceStartCommand())
	cmd.AddCommand(newServeServiceStopCommand())
	cmd.AddCommand(newServeServiceStatusCommand())
	return cmd
}

func newServeServiceInstallCommand() *cobra.Command {
	var opts serveServiceInstallOpts

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install boxy serve as a background service (real service by default; --user for an unprivileged fallback)",
		Long: `Installs boxy serve as an OS-managed background service so it starts
automatically and survives logout/reboot.

By default this registers a real service (Windows Service via SCM, Linux
systemd system unit) and requires an elevated process (Administrator /
root). Pass --user to install the unprivileged fallback instead (Windows
Task Scheduler at-logon task, Linux systemd user unit) — note this starts
at user logon, not at machine boot before any login.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServeServiceInstall(cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.userMode, "user", false, "install the unprivileged fallback (no admin/root required) instead of a real service")
	cmd.Flags().StringVar(&opts.serveOpts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	cmd.Flags().StringVar(&opts.serveOpts.listen, "listen", "", "HTTP listen address (default :9090)")
	cmd.Flags().BoolVar(&opts.serveOpts.ui, "ui", true, "enable web dashboard UI")
	cmd.Flags().StringVar(&opts.serveOpts.grpcListen, "grpc-listen", "", "agent gRPC listen address (default :9091)")
	cmd.Flags().StringArrayVar(&opts.serveOpts.grpcCertSANs, "grpc-cert-san", nil, "extra DNS name or IP to include in the agent gRPC server certificate SANs (repeatable)")
	cmd.Flags().BoolVar(&opts.serveOpts.insecure, "insecure", false, "serve agent gRPC without TLS/mTLS (local development only)")
	return cmd
}

func runServeServiceInstall(cmd *cobra.Command, opts serveServiceInstallOpts) error {
	if !opts.userMode {
		elevated, err := isElevatedFn()
		if err != nil {
			return fmt.Errorf("check process privilege: %w", err)
		}
		if !elevated {
			return fmt.Errorf("installing a real boxy-serve service requires an elevated process (run as Administrator, or as root/sudo) — pass --user to install the unprivileged fallback instead")
		}
	}

	boxyDir := opts.boxyDir
	if boxyDir == "" {
		statePath, err := serveStatePath(opts.serveOpts.configPath)
		if err != nil {
			return err
		}
		boxyDir = filepath.Dir(statePath)
	}
	absConfigPath, err := resolveAbs(opts.serveOpts.configPath)
	if err != nil {
		return err
	}
	logFile := filepath.Join(boxyDir, "service.log")

	cfg := serveServiceConfig{
		ConfigPath:   absConfigPath,
		Listen:       opts.serveOpts.listen,
		UI:           opts.serveOpts.ui,
		GRPCListen:   opts.serveOpts.grpcListen,
		GRPCCertSANs: stringsOf(opts.serveOpts.grpcCertSANs),
		Insecure:     opts.serveOpts.insecure,
		LogFile:      logFile,
	}
	cfgPath := filepath.Join(boxyDir, "service.yaml")
	if err := saveServeServiceConfig(cfgPath, cfg); err != nil {
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
		Name:        serveServiceName,
		DisplayName: "Boxy Server",
		Description: "Boxy daemon — API server, reconcile loop, and embedded agent",
		ExecPath:    exePath,
		// --log-file must be a literal arg, not just recorded in the
		// service config: the root command's --log-file is a persistent
		// flag read directly off this process's cmdline (root.go's
		// PersistentPreRunE), so a service invocation with only
		// --service-config on the args list never picks up cfg.LogFile.
		Args: []string{"serve", "--service-config", cfgPath, "--log-file", logFile},
	}
	if err := mgr.Install(spec); err != nil {
		return fmt.Errorf("install %s service: %w", serveServiceName, err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ boxy-serve installed and started (config: %s, log: %s)\n", cfgPath, logFile)
	return nil
}

func newServeServiceUninstallCommand() *cobra.Command {
	var purge bool
	var boxyDir string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the installed boxy serve service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedBoxyDir := boxyDir
			if resolvedBoxyDir == "" {
				statePath, err := serveStatePath("")
				if err != nil {
					return err
				}
				resolvedBoxyDir = filepath.Dir(statePath)
			}
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Uninstall(serveServiceName); err != nil {
				return fmt.Errorf("uninstall %s service: %w", serveServiceName, err)
			}
			if purge {
				if err := os.RemoveAll(resolvedBoxyDir); err != nil {
					return fmt.Errorf("remove state directory %q: %w", resolvedBoxyDir, err)
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-serve service uninstalled")
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove boxy serve's state directory (.boxy/)")
	cmd.Flags().StringVar(&boxyDir, "boxy-dir", "", "the .boxy/ state directory, to locate it for --purge (default resolved the same way `boxy serve` resolves it)")
	return cmd
}

func newServeServiceStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the installed boxy serve service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Start(serveServiceName); err != nil {
				return fmt.Errorf("start %s service: %w", serveServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-serve service started")
			return nil
		},
	}
}

func newServeServiceStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the installed boxy serve service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Stop(serveServiceName); err != nil {
				return fmt.Errorf("stop %s service: %w", serveServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-serve service stopped")
			return nil
		},
	}
}

func newServeServiceStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the installed boxy serve service's status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServeServiceStatus(cmd)
		},
	}
}

func runServeServiceStatus(cmd *cobra.Command) error {
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	st, err := mgr.Status(serveServiceName)
	if err != nil {
		return fmt.Errorf("get %s service status: %w", serveServiceName, err)
	}
	if !st.Installed {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "boxy-serve: not installed")
		return nil
	}
	state := "stopped"
	if st.Running {
		state = "running"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "boxy-serve: %s (%s)\n", state, st.Mode)
	return nil
}
