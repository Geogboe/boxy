package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Geogboe/boxy/internal/userconfig"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type clientConfig struct {
	Server string `yaml:"server,omitempty"`
}

func clientConfigPath() (string, error) {
	root, err := userconfig.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "client.yaml"), nil
}

func loadClientConfig() (clientConfig, error) {
	path, err := clientConfigPath()
	if err != nil {
		return clientConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return clientConfig{}, nil
		}
		return clientConfig{}, fmt.Errorf("read client config %q: %w", path, err)
	}

	var cfg clientConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return clientConfig{}, fmt.Errorf("parse client config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return clientConfig{}, fmt.Errorf("parse client config %q: multiple YAML documents are not supported", path)
		}
		return clientConfig{}, fmt.Errorf("parse client config %q: %w", path, err)
	}
	if cfg.Server != "" {
		cfg.Server, err = normalizeClientServer(cfg.Server)
		if err != nil {
			return clientConfig{}, fmt.Errorf("validate client config %q: %w", path, err)
		}
	}
	return cfg, nil
}

func writeClientConfig(cfg clientConfig) error {
	if cfg.Server != "" {
		server, err := normalizeClientServer(cfg.Server)
		if err != nil {
			return err
		}
		cfg.Server = server
	}
	path, err := clientConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create client config directory: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode client config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".client.yaml-*")
	if err != nil {
		return fmt.Errorf("create client config temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set client config temporary file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write client config temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close client config temporary file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		// Windows does not replace an existing destination through Rename.
		// Remove only this exact runtime config path, then retry the same
		// temporary-file rename so the normal path remains atomic.
		if runtime.GOOS != "windows" {
			return fmt.Errorf("replace client config %q: %w", path, err)
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("replace client config %q: %w (remove existing: %v)", path, err, removeErr)
		}
		if retryErr := os.Rename(tmpPath, path); retryErr != nil {
			return fmt.Errorf("replace client config %q: %w", path, retryErr)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set client config permissions: %w", err)
	}
	return nil
}

func normalizeClientServer(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("server URL must not be empty")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("server URL must include an http or https host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("server URL must not include credentials, query parameters, or fragments")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("server URL must not include a path")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = ""
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func resolveClientServer(cmd *cobra.Command, explicit string) (string, error) {
	if cmd != nil {
		if flag := cmd.Flags().Lookup("server"); flag != nil && flag.Changed {
			return explicit, nil
		}
	}
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	if raw := strings.TrimSpace(os.Getenv("BOXY_SERVER")); raw != "" {
		return normalizeClientServer(raw)
	}
	cfg, err := loadClientConfig()
	if err != nil {
		return "", err
	}
	return cfg.Server, nil
}

func isAgentTransportCommand(cmd *cobra.Command) bool {
	parts := strings.Fields(cmd.CommandPath())
	return len(parts) >= 3 && parts[1] == "agent" && (parts[2] == "serve" || parts[2] == "service")
}

func resolveClientServerFlag(cmd *cobra.Command) error {
	if isAgentTransportCommand(cmd) {
		return nil
	}
	flag := cmd.Flags().Lookup("server")
	if flag == nil || flag.Changed {
		return nil
	}
	var explicit string
	value, err := cmd.Flags().GetString("server")
	if err == nil {
		explicit = value
	}
	if cmd.Name() == "status" {
		configPath, _ := cmd.Flags().GetString("config")
		if strings.TrimSpace(os.Getenv("BOXY_SERVER")) == "" && configPath != "" {
			cfg, _, loadErr := loadConfig(configPath)
			if loadErr != nil {
				return loadErr
			}
			if cfg.Server.Listen != "" {
				explicit = secureServerURL(displayAddr(cfg.Server.Listen))
			}
		}
	}
	resolved, err := resolveClientServer(cmd, explicit)
	if err != nil {
		return err
	}
	if resolved != "" {
		return flag.Value.Set(resolved)
	}
	return nil
}

func newConfigClientCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Manage the global CLI server default",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigClientShowCommand())
	cmd.AddCommand(newConfigClientSetServerCommand())
	return cmd
}

func newConfigClientShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the global CLI server default",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadClientConfig()
			if err != nil {
				return err
			}
			value := cfg.Server
			if value == "" {
				value = "(not set)"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "server: %s\n", value)
			return nil
		},
	}
}

func newConfigClientSetServerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set-server <url>",
		Short: "Set the global CLI server default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := normalizeClientServer(args[0])
			if err != nil {
				return err
			}
			cfg, err := loadClientConfig()
			if err != nil {
				return err
			}
			cfg.Server = server
			if err := writeClientConfig(cfg); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "server: %s\n", server)
			return nil
		},
	}
}
