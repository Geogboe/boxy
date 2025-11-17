package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/Geogboe/boxy/internal/core/pool"
)

// Config represents the Boxy configuration
type Config struct {
	Pools   []pool.PoolConfig `yaml:"pools" json:"pools"`
	Storage StorageConfig     `yaml:"storage" json:"storage"`
	Logging LoggingConfig     `yaml:"logging" json:"logging"`
}

// StorageConfig contains storage configuration
type StorageConfig struct {
	Type   string `yaml:"type" json:"type"`     // sqlite, postgres
	Path   string `yaml:"path" json:"path"`     // for sqlite
	DSN    string `yaml:"dsn" json:"dsn"`       // for postgres
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level" json:"level"`   // debug, info, warn, error
	Format string `yaml:"format" json:"format"` // text, json
}

// Load loads configuration from file
func Load(configPath string) (*Config, error) {
	// Set up viper
	v := viper.New()
	v.SetConfigType("yaml")

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
	v.SetDefault("storage.path", filepath.Join(os.Getenv("HOME"), ".config", "boxy", "boxy.db"))
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "text")

	// Read config
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate pools
	for i, poolCfg := range cfg.Pools {
		if err := poolCfg.Validate(); err != nil {
			return nil, fmt.Errorf("pool %d (%s) invalid: %w", i, poolCfg.Name, err)
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
	return filepath.Join(os.Getenv("HOME"), ".config", "boxy", "boxy.yaml")
}

// GetDefaultDBPath returns the default database path
func GetDefaultDBPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "boxy", "boxy.db")
}

// EnsureConfigDir ensures the config directory exists
func EnsureConfigDir() error {
	configDir := filepath.Join(os.Getenv("HOME"), ".config", "boxy")
	return os.MkdirAll(configDir, 0755)
}
