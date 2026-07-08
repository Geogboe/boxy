package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Boxy configuration in the current directory",
		Long:  "Creates a comprehensive commented boxy.yaml starter configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(force)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing boxy.yaml")
	return cmd
}

const configFileName = "boxy.yaml"

func runInit(force bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	dest := filepath.Join(cwd, configFileName)

	if !force {
		if _, err := os.Stat(dest); err == nil {
			return errors.New("boxy.yaml already exists (use --force to overwrite)")
		}
	}

	if err := os.WriteFile(dest, []byte(starterConfig), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configFileName, err)
	}

	fmt.Fprintf(os.Stderr, "  Created %s\n", configFileName)
	fmt.Fprintf(os.Stderr, "\n  Next steps:\n")
	fmt.Fprintf(os.Stderr, "    1. Edit boxy.yaml to define your pools\n")
	fmt.Fprintf(os.Stderr, "    2. boxy config validate     Validate your config\n")
	fmt.Fprintf(os.Stderr, "    3. boxy serve               Start the daemon\n\n")
	return nil
}

const starterConfig = `---
# Boxy configuration
#
# This starter file is intentionally verbose. Keep the sections you need,
# delete the rest, and uncomment examples as your setup grows.
#
# Top-level sections:
#   - server: daemon listen address and web UI settings (the HTTP API is
#     always served alongside the UI — there is no separate api: section)
#   - providers: named driver instances available to boxy serve
#   - pools: warm resource inventories Boxy reconciles for you
#
# Logging is configured with CLI flags rather than boxy.yaml:
#   boxy serve --log-level debug --log-file ./boxy.log

server:
  listen: ":9090"
  # ui: true                    # Web dashboard, served at "/" (default: enabled)
  # providers: [docker, hyperv] # Optional embedded provider type hints

providers:
  # Named provider instances. Pools can bind to these with provider: <name>.
  - name: docker-local
    type: docker
    # config:
    #   host: unix:///var/run/docker.sock

  # Hyper-V provider example. Uncomment when running Boxy on a Windows host
  # with Hyper-V available.
  # - name: hyperv-local
  #   type: hyperv

pools:
  # Docker/container pool example.
  - name: alpine-dev
    type: docker
    # provider: docker-local
    config:
      image: alpine:latest
      command: ["/bin/sh", "-c", "while true; do sleep 3600; done"]
      # env:
      #   DEMO_MODE: "true"
      # labels:
      #   purpose: starter
      # ports:
      #   - "8080:80"
      # cpu: "1.0"
      # memory: 512m
    # policy: only preheat and recycle are recognized here today.
    policy:
      preheat:
        min_ready: 1 # keep this many resources ready (warm) at all times
        max_total: 3 # hard cap on total resources for this pool
      recycle:
        max_age: 4h # destroy + replace ready resources older than this

  # Hyper-V VM pool example.
  # Use type: vm for VM inventory, then point provider: at a Hyper-V
  # provider instance (or directly at the hyperv driver type).
  # - name: win2022-base
  #   type: vm
  #   provider: hyperv-local
  #   config:
  #     template_vhd: "C:\\HyperV\\Images\\windows-2022-base.vhdx"
  #     vhd_dir: "C:\\HyperV\\Boxy"
  #     generation: 2
  #     cpu_count: 4
  #     memory_mb: 8192
  #     switch: "LabSwitch"
  #     guest_os: windows
  #     guest_user: Administrator
  #     guest_password_ref: env:BOXY_HYPERV_GUEST_PASSWORD
  #   policy:
  #     preheat:
  #       min_ready: 2
  #       max_total: 5
  #     recycle:
  #       max_age: 168h
`
