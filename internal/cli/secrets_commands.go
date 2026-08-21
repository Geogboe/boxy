package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/spf13/cobra"
)

type secretCommandOpts struct {
	configPath string
	backend    string
	path       string
	service    string
}

func newDoctorCommand() *cobra.Command {
	var opts secretCommandOpts
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check local Boxy state and secret backend readiness",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd, opts)
		},
	}
	addSecretCommandFlags(cmd, &opts)
	return cmd
}

func newMigrateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run explicit local state migrations",
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	var opts secretCommandOpts
	secretsCmd := &cobra.Command{
		Use:   "secrets",
		Short: "Move legacy plaintext guest credentials to the selected backend",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrateSecrets(cmd.Context(), cmd, opts)
		},
	}
	addSecretCommandFlags(secretsCmd, &opts)
	cmd.AddCommand(secretsCmd)
	return cmd
}

func addSecretCommandFlags(cmd *cobra.Command, opts *secretCommandOpts) {
	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	cmd.Flags().StringVar(&opts.backend, "backend", "", "explicit secret backend override (file, keyring, or dpapi)")
	cmd.Flags().StringVar(&opts.path, "path", "", "secret backend file path override")
	cmd.Flags().StringVar(&opts.service, "service", "", "keyring service name override")
}

func runDoctor(ctx context.Context, cmd *cobra.Command, opts secretCommandOpts) error {
	cfg, cfgPath, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	statePath, err := serveStatePath(cfgPath)
	if err != nil {
		return err
	}
	st, err := store.NewDiskStore(statePath)
	if err != nil {
		return err
	}
	legacy, ok := any(st).(store.LegacyPoolGuestCredentialStore)
	if !ok {
		return fmt.Errorf("state store does not support secret migration")
	}
	legacyValues, err := legacy.ListPoolGuestCredentials(ctx)
	if err != nil {
		return fmt.Errorf("list legacy credentials: %w", err)
	}

	spec := cfg.Server.Secrets
	applySecretOverrides(&spec, cmd, opts)
	if !spec.Configured() {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "secret backend: not configured")
		if len(legacyValues) != 0 {
			return fmt.Errorf("%d legacy plaintext pool credential(s) found; configure a backend and run `boxy migrate secrets`", len(legacyValues))
		}
		return nil
	}
	secretStore, err := openConfiguredSecretStore(spec, statePath, true)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "secret backend: %s (ready)\n", spec.Backend)
	if len(legacyValues) != 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "legacy plaintext credentials: %d (migration required)\n", len(legacyValues))
		return fmt.Errorf("legacy plaintext credentials remain; run `boxy migrate secrets`")
	}
	_ = secretStore
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "legacy plaintext credentials: none")
	return nil
}

func runMigrateSecrets(ctx context.Context, cmd *cobra.Command, opts secretCommandOpts) error {
	cfg, cfgPath, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	statePath, err := serveStatePath(cfgPath)
	if err != nil {
		return err
	}
	st, err := store.NewDiskStore(statePath)
	if err != nil {
		return err
	}
	legacy, ok := any(st).(store.LegacyPoolGuestCredentialStore)
	if !ok {
		return fmt.Errorf("state store does not support secret migration")
	}
	spec := cfg.Server.Secrets
	applySecretOverrides(&spec, cmd, opts)
	if !spec.Configured() {
		return boxysecrets.ErrBackendRequired
	}
	secretStore, err := openConfiguredSecretStore(spec, statePath, true)
	if err != nil {
		return err
	}
	legacyValues, err := legacy.ListPoolGuestCredentials(ctx)
	if err != nil {
		return fmt.Errorf("list legacy credentials: %w", err)
	}
	pools := make([]string, 0, len(legacyValues))
	for pool := range legacyValues {
		pools = append(pools, string(pool))
	}
	sort.Strings(pools)
	for _, pool := range pools {
		name := modelPoolName(pool)
		value := []byte(legacyValues[name])
		key := boxysecrets.PoolBootstrapKey(pool)
		current, getErr := secretStore.Get(ctx, key)
		if getErr == nil && string(current) != string(value) {
			return fmt.Errorf("secret backend already contains a different value for pool %q", pool)
		}
		if getErr != nil && !errors.Is(getErr, boxysecrets.ErrNotFound) {
			return fmt.Errorf("check migrated credential for pool %q: %w", pool, getErr)
		}
		if getErr != nil {
			if err := secretStore.Put(ctx, key, value); err != nil {
				return fmt.Errorf("write credential for pool %q: %w", pool, err)
			}
		}
		verified, err := secretStore.Get(ctx, key)
		if err != nil || string(verified) != string(value) {
			return fmt.Errorf("verify migrated credential for pool %q", pool)
		}
		if err := legacy.DeletePoolGuestCredential(ctx, name); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("remove legacy credential for pool %q: %w", pool, err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "migrated pool %s\n", pool)
	}
	if len(pools) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no legacy plaintext credentials found")
	}
	return nil
}

func applySecretOverrides(spec *boxyconfig.SecretSpec, cmd *cobra.Command, opts secretCommandOpts) {
	if cmd.Flags().Changed("backend") {
		spec.Backend = opts.backend
	}
	if cmd.Flags().Changed("path") {
		spec.Path = opts.path
	}
	if cmd.Flags().Changed("service") {
		spec.Service = opts.service
	}
}

func modelPoolName(name string) model.PoolName { return model.PoolName(name) }
