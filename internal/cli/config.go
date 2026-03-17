package cli

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newConfigValidateCommand())
	return cmd
}

type configValidateOpts struct {
	configPath string
}

func newConfigValidateCommand() *cobra.Command {
	var opts configValidateOpts

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigValidate(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	return cmd
}

func runConfigValidate(ctx context.Context, opts configValidateOpts) error {
	cfg, cfgPath, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	if cfgPath == "" {
		return fmt.Errorf("no config file found")
	}

	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return fmt.Errorf("register builtin providers: %w", err)
	}

	if err := reg.ValidateInstances(ctx, cfg.Providers); err != nil {
		return fmt.Errorf("validate providers: %w", err)
	}

	fmt.Println("config OK")
	return nil
}
