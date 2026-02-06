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
// # Schema
//
// A JSON Schema (draft 2020-12) describing valid boxy.yaml files is embedded
// in the binary and exposed via [SchemaJSON]. The schema file name used on
// disk is [SchemaFileName] (.boxy-schema.json). The `boxy init` command writes
// this file alongside the config and adds a yaml-language-server directive so
// editors with the YAML extension provide autocompletion and validation.
//
// # Encryption
//
// The package provides encryption key management:
//
//	key, err := config.GetEncryptionKey()
//	encryptor, err := crypto.NewEncryptor(key)
package config
