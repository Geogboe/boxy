// Package config provides configuration loading and management.
//
// Configuration is loaded from YAML files with support for environment variable
// overrides. The package searches standard locations and applies sensible defaults.
//
// # Search Paths
//
// Configuration files are searched in order:
//  1. ./boxy.yaml
//  2. ~/.config/boxy/boxy.yaml
//  3. /etc/boxy/boxy.yaml
//
// # Environment Variables
//
// All config values can be overridden with BOXY_ prefixed environment variables:
//
//	BOXY_STORAGE_PATH=/custom/path
//	BOXY_API_ENABLED=true
//	BOXY_API_LISTEN=:9000
//
// # Configuration Structure
//
//	pools:              # Resource pool configurations
//	agents:             # Remote agent configurations
//	storage:            # Storage backend (SQLite)
//	logging:            # Log level and format
//	api:                # HTTP API settings
//	encryption_key:     # Base64 encryption key
//
// # Defaults
//
//	storage:
//	  type: sqlite
//	  path: ~/.config/boxy/boxy.db
//	logging:
//	  level: info
//	api:
//	  enabled: true
//	  listen: :8080
//	  read_timeout_secs: 10
//	  write_timeout_secs: 10
//	  idle_timeout_secs: 60
//
// # Example
//
//	cfg, err := config.Load()
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Access configuration
//	for _, pool := range cfg.Pools {
//		fmt.Printf("Pool: %s (backend: %s)\n", pool.Name, pool.Backend)
//	}
//
// # Encryption
//
// The package provides encryption key management:
//
//	key, err := config.GetEncryptionKey()
//	encryptor, err := crypto.NewEncryptor(key)
package config
