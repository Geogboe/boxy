// internal/cli/service_config.go
package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"gopkg.in/yaml.v3"
)

// agentServiceConfig is the on-disk shape of an installed agent service's
// resolved configuration (written by `boxy agent service install`, read by
// `boxy agent serve --service-config <path>`). Paths owned by the service
// invocation (data dir, CA cert, and log file) are stored absolute because a
// service has no predictable working directory. ProviderConfigs remain
// opaque provider-specific values; path fields inside them must be made
// absolute by the operator when the provider requires it. Token is the
// agent's single-use bootstrap token, base64(svcmgr.EncryptToken(...))-encoded
// at rest and scrubbed to empty once the agent successfully registers (see
// scrubAgentServiceConfigToken and its call site in agent_serve.go's
// OnRegistered callback).
type agentServiceConfig struct {
	Server          string                 `yaml:"server"`
	Providers       []string               `yaml:"providers"`
	ProviderConfigs []providersdk.Instance `yaml:"provider_configs,omitempty"`
	Token           string                 `yaml:"token,omitempty"`
	Name            string                 `yaml:"name,omitempty"`
	CACert          string                 `yaml:"ca_cert,omitempty"`
	DataDir         string                 `yaml:"data_dir"`
	Insecure        bool                   `yaml:"insecure,omitempty"`
	LogFile         string                 `yaml:"log_file"`
}

func saveAgentServiceConfig(path string, cfg agentServiceConfig) error {
	if cfg.Token != "" {
		enc, err := svcmgr.EncryptToken([]byte(cfg.Token))
		if err != nil {
			return fmt.Errorf("encrypt token: %w", err)
		}
		cfg.Token = base64.StdEncoding.EncodeToString(enc)
	}
	return writeYAMLFile(path, cfg)
}

func loadAgentServiceConfig(path string) (agentServiceConfig, error) {
	var cfg agentServiceConfig
	if err := readYAMLFile(path, &cfg); err != nil {
		return agentServiceConfig{}, err
	}
	if cfg.Token != "" {
		raw, err := base64.StdEncoding.DecodeString(cfg.Token)
		if err != nil {
			return agentServiceConfig{}, fmt.Errorf("decode stored token: %w", err)
		}
		dec, err := svcmgr.DecryptToken(raw)
		if err != nil {
			return agentServiceConfig{}, fmt.Errorf("decrypt stored token: %w", err)
		}
		cfg.Token = string(dec)
	}
	return cfg, nil
}

// scrubAgentServiceConfigToken clears the token field of an already-saved
// agent service config in place, leaving every other field untouched.
// Called once the agent successfully registers — the token is single-use
// and worthless after that point, so nothing sensitive should linger in
// the file past bootstrap.
func scrubAgentServiceConfigToken(path string) error {
	cfg, err := loadAgentServiceConfig(path)
	if err != nil {
		return err
	}
	if cfg.Token == "" {
		return nil
	}
	cfg.Token = ""
	return saveAgentServiceConfig(path, cfg)
}

// serveServiceConfig is the on-disk shape of an installed serve service's
// resolved configuration. It has no secret field — serve has no bootstrap
// token equivalent.
type serveServiceConfig struct {
	ConfigPath   string   `yaml:"config_path,omitempty"`
	Listen       string   `yaml:"listen,omitempty"`
	UI           bool     `yaml:"ui"`
	GRPCListen   string   `yaml:"grpc_listen,omitempty"`
	GRPCCertSANs []string `yaml:"grpc_cert_sans,omitempty"`
	Insecure     bool     `yaml:"insecure,omitempty"`
	LogFile      string   `yaml:"log_file"`
}

func saveServeServiceConfig(path string, cfg serveServiceConfig) error {
	return writeYAMLFile(path, cfg)
}

func loadServeServiceConfig(path string) (serveServiceConfig, error) {
	var cfg serveServiceConfig
	err := readYAMLFile(path, &cfg)
	return cfg, err
}

func writeYAMLFile(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create directory for %q: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	// os.WriteFile only honors the mode argument if the file does not exist.
	// On rewrite of a pre-existing file, the existing permissions are retained.
	// Explicitly enforce 0o600 to ensure the token file is never left with
	// loosened permissions (critical on Linux where 0o600 is the only protection).
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %q to 0600: %w", path, err)
	}
	return nil
}

func readYAMLFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	return nil
}

// resolveAbs resolves path to an absolute path. Empty input stays empty —
// optional fields (e.g. --ca-cert, --config) that were never set must not
// be turned into a spurious cwd-relative path.
func resolveAbs(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", path, err)
	}
	return abs, nil
}
