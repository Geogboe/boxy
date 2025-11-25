package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/pkg/crypto"
)

// Config represents the Boxy configuration
type Config struct {
	Pools   []pool.PoolConfig `yaml:"pools" json:"pools"`
	Agents  []AgentConfig     `yaml:"agents" json:"agents"`
	Storage StorageConfig     `yaml:"storage" json:"storage"`
	Logging LoggingConfig     `yaml:"logging" json:"logging"`
	API     APIConfig         `yaml:"api" json:"api"`
}

// AgentConfig contains remote agent configuration
type AgentConfig struct {
	ID          string   `yaml:"id" json:"id"`                       // Unique agent ID
	Address     string   `yaml:"address" json:"address"`             // host:port
	Providers   []string `yaml:"providers" json:"providers"`         // List of provider names on this agent
	TLSCertPath string   `yaml:"tls_cert_path" json:"tls_cert_path"` // Client certificate
	TLSKeyPath  string   `yaml:"tls_key_path" json:"tls_key_path"`   // Client key
	TLSCAPath   string   `yaml:"tls_ca_path" json:"tls_ca_path"`     // CA certificate
	UseTLS      bool     `yaml:"use_tls" json:"use_tls"`             // Enable TLS
}

// StorageConfig contains storage configuration
type StorageConfig struct {
	Type string `yaml:"type" json:"type"` // sqlite, postgres
	Path string `yaml:"path" json:"path"` // for sqlite
	DSN  string `yaml:"dsn" json:"dsn"`   // for postgres
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level" json:"level"`   // debug, info, warn, error
	Format string `yaml:"format" json:"format"` // text, json
}

// APIConfig controls the embedded HTTP API server
type APIConfig struct {
	Enabled          bool   `yaml:"enabled" json:"enabled"`
	Listen           string `yaml:"listen" json:"listen"`                         // e.g. :8080
	ReadTimeoutSecs  int    `yaml:"read_timeout_secs" json:"read_timeout_secs"`   // server read timeout
	WriteTimeoutSecs int    `yaml:"write_timeout_secs" json:"write_timeout_secs"` // server write timeout
	IdleTimeoutSecs  int    `yaml:"idle_timeout_secs" json:"idle_timeout_secs"`   // keep-alive timeout
}

// Load loads configuration from file
func Load(configPath string) (*Config, error) {
	// Set up viper
	v := viper.New()
	v.SetConfigType("yaml")

	// Enable environment variable support
	// Environment variables can override config file values
	// Example: BOXY_STORAGE_TYPE=postgres, BOXY_STORAGE_DSN=...
	v.SetEnvPrefix("BOXY")
	v.AutomaticEnv()

	// If config path provided, use it
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		// Search for config in standard locations
		v.SetConfigName("boxy")
		v.AddConfigPath(".")
		v.AddConfigPath(filepath.Join(os.Getenv("HOME"), ".config", "boxy"))
		v.AddConfigPath("/etc/boxy")
	}

	// Set defaults
	v.SetDefault("storage.type", "sqlite")
	v.SetDefault("storage.path", "./boxy.db")
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "text")
	v.SetDefault("api.enabled", true)
	v.SetDefault("api.listen", ":8080")
	v.SetDefault("api.read_timeout_secs", 15)
	v.SetDefault("api.write_timeout_secs", 15)
	v.SetDefault("api.idle_timeout_secs", 60)

	// Read config file (optional - will use defaults and env vars if not found)
	if err := v.ReadInConfig(); err != nil {
		// Only return error if config was explicitly specified
		if configPath != "" {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
		// Otherwise, config file is optional - use defaults + env vars
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate pools
	for i := range cfg.Pools {
		// Apply defaults
		cfg.Pools[i].ApplyDefaults()

		// Validate
		if err := cfg.Pools[i].Validate(); err != nil {
			return nil, fmt.Errorf("pool %d (%s) invalid: %w", i, cfg.Pools[i].Name, err)
		}
	}

	return &cfg, nil
}

// LoadFromBytes loads configuration from byte slice
func LoadFromBytes(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

// GetDefaultConfigPath returns the default config file path
func GetDefaultConfigPath() string {
	return "./boxy.yaml"
}

// GetDefaultDBPath returns the default database path
func GetDefaultDBPath() string {
	return "./boxy.db"
}

// EnsureConfigDir ensures the config directory exists
// Note: This is deprecated - config and data should be local
func EnsureConfigDir() error {
	configDir := filepath.Join(os.Getenv("HOME"), ".config", "boxy")
	return os.MkdirAll(configDir, 0750)
}

// GetEncryptionKey gets or creates the encryption key
// Priority: BOXY_ENCRYPTION_KEY env var > stored key file > generate new
func GetEncryptionKey() ([]byte, error) {
	// Try environment variable first
	if envKey := os.Getenv("BOXY_ENCRYPTION_KEY"); envKey != "" {
		key, err := base64.StdEncoding.DecodeString(envKey)
		if err != nil {
			return nil, fmt.Errorf("invalid BOXY_ENCRYPTION_KEY format (must be base64): %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("BOXY_ENCRYPTION_KEY must be 32 bytes (got %d)", len(key))
		}
		return key, nil
	}

	// Try to load from file
	keyPath := GetEncryptionKeyPath()
	// #nosec G304 - keyPath is a local constant, not user input
	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := base64.StdEncoding.DecodeString(string(data))
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}

	// Generate new key and store it
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	// Save key to file with restrictive permissions
	keyB64 := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(keyB64), 0600); err != nil {
		return nil, fmt.Errorf("failed to save encryption key: %w", err)
	}

	return key, nil
}

// GetEncryptionKeyPath returns the path to the encryption key file
func GetEncryptionKeyPath() string {
	return "./encryption.key"
}
