# 11: Config File Location

---

## Metadata

```yaml
feature: "Config Location Fix"
slug: "config-location"
status: "not-started"
priority: "high"
type: "fix"
effort: "small"
depends_on: []
enables: ["project-specific-config", "docker-deployment"]
testing: ["integration", "e2e"]
breaking_change: true
week: "5-6"
related_docs:
  - "12-docker-compose.md"
```

---

## Overview

**Current problem**: Config at `~/.config/boxy/boxy.yaml` (user-specific)
**Correct approach**: Config at `./boxy.yaml` (project-specific)

**Rationale**: Boxy is a **service orchestrator**, not a user CLI tool. Config should be project-specific like Docker Compose, not user-specific like Git.

---

## Problem with Current Approach

```
~/.config/boxy/boxy.yaml    ❌ WRONG
```

**Issues:**
- Multiple projects can't have different configs
- Service deployments (systemd, Docker) awkward
- Config tied to user account, not project
- Doesn't match "infrastructure as code" paradigm
- Hard to version control config

**Example failure case:**
```bash
# User wants two Boxy instances with different configs
cd /opt/boxy-prod
boxy serve    # Uses ~/.config/boxy/boxy.yaml

cd /opt/boxy-dev
boxy serve    # SAME config! ❌
```

---

## Correct Approach

```
./boxy.yaml                 ✅ CORRECT
```

**Benefits:**
- Project-specific: Each directory can have its own config
- Version control: Config lives with project
- Docker-friendly: Mount config easily
- Clear ownership: Config belongs to project, not user
- Standard practice: Matches docker-compose.yml, k8s manifests, etc.

**Example success case:**
```bash
# Two Boxy instances with different configs
cd /opt/boxy-prod
cat boxy.yaml    # Production config
boxy serve       # Uses ./boxy.yaml ✅

cd /opt/boxy-dev
cat boxy.yaml    # Development config
boxy serve       # Uses ./boxy.yaml ✅
```

---

## Config Discovery Strategy

**Priority order:**

1. **Explicit flag** (highest priority)
   ```bash
   boxy serve --config /path/to/custom.yaml
   ```

2. **Current directory** (project-specific)
   ```bash
   ./boxy.yaml
   ```

3. **Error if not found**
   ```bash
   Error: config file not found
   Searched: ./boxy.yaml
   Use: boxy serve --config /path/to/config.yaml
   ```

**No fallback to ~/.config/** - Forces explicit config management

---

## Implementation

### Task 11.1: Update Config Loading

**File**: `internal/config/loader.go`

```go
package config

import (
    "fmt"
    "os"
    "path/filepath"
)

// LoadConfig loads configuration from file
func LoadConfig(explicitPath string) (*Config, error) {
    var configPath string

    if explicitPath != "" {
        // 1. Explicit flag has highest priority
        configPath = explicitPath
    } else {
        // 2. Current directory
        if _, err := os.Stat("./boxy.yaml"); err == nil {
            configPath = "./boxy.yaml"
        } else {
            return nil, fmt.Errorf("config file not found: ./boxy.yaml\n" +
                "Create a config file or specify: boxy serve --config /path/to/config.yaml")
        }
    }

    // Verify file exists
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        return nil, fmt.Errorf("config file not found: %s", configPath)
    }

    // Load and parse
    data, err := os.ReadFile(configPath)
    if err != nil {
        return nil, fmt.Errorf("failed to read config: %w", err)
    }

    config := &Config{}
    if err := yaml.Unmarshal(data, config); err != nil {
        return nil, fmt.Errorf("failed to parse config: %w", err)
    }

    // Store resolved path
    config.ConfigPath = filepath.Abs(configPath)

    return config, nil
}
```

---

### Task 11.2: Update CLI Commands

**File**: `cmd/boxy/commands/serve.go`

```go
var serveCmd = &cobra.Command{
    Use:   "serve",
    Short: "Start Boxy server",
    RunE: func(cmd *cobra.Command, args []string) error {
        configPath, _ := cmd.Flags().GetString("config")

        // Load config (will error if not found)
        config, err := config.LoadConfig(configPath)
        if err != nil {
            return err
        }

        // Start server
        server := server.New(config)
        return server.Start()
    },
}

func init() {
    serveCmd.Flags().StringP("config", "c", "", "Config file path (default: ./boxy.yaml)")
}
```

---

### Task 11.3: Update Database Location

**Database should also be project-specific:**

```yaml
# boxy.yaml
storage:
  type: sqlite
  path: ./data/boxy.db    # Relative to config file ✅
  # NOT: ~/.local/share/boxy/boxy.db ❌
```

**Implementation:**
```go
func resolveDBPath(configPath, dbPath string) string {
    if filepath.IsAbs(dbPath) {
        return dbPath  // Already absolute
    }

    // Resolve relative to config file location
    configDir := filepath.Dir(configPath)
    return filepath.Join(configDir, dbPath)
}
```

---

### Task 11.4: Update Examples

**All example configs should use project-relative paths:**

```yaml
# examples/docker-pool.yaml
storage:
  type: sqlite
  path: ./data/boxy.db       # Relative ✅

logging:
  level: info
  output: ./logs/boxy.log    # Relative ✅

pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04
```

---

## Migration from Old Location

### Automated Migration

```bash
# Warn if old config found
if [ -f ~/.config/boxy/boxy.yaml ]; then
    echo "⚠️  WARNING: Config found at ~/.config/boxy/boxy.yaml"
    echo "This location is deprecated. Copy to project directory:"
    echo ""
    echo "  cp ~/.config/boxy/boxy.yaml ./boxy.yaml"
    echo ""
    echo "Then update paths to be project-relative."
fi
```

### Manual Migration Steps

```bash
# 1. Copy config to project directory
cp ~/.config/boxy/boxy.yaml /opt/boxy/boxy.yaml

# 2. Update paths to be relative
cd /opt/boxy
vim boxy.yaml

# Change:
#   path: /home/user/.local/share/boxy/boxy.db
# To:
#   path: ./data/boxy.db

# 3. Create data directory
mkdir -p /opt/boxy/data

# 4. Copy database (if needed)
cp ~/.local/share/boxy/boxy.db /opt/boxy/data/boxy.db

# 5. Test
boxy serve
```

---

## Docker/Systemd Integration

### Docker Compose

```yaml
# docker-compose.yml
version: '3.8'
services:
  boxy:
    image: ghcr.io/geogboe/boxy:v1
    volumes:
      - ./boxy.yaml:/app/boxy.yaml:ro      # Mount config ✅
      - ./data:/app/data                   # Mount data dir ✅
    working_dir: /app
    command: serve
```

### Systemd Service

```ini
# /etc/systemd/system/boxy.service
[Unit]
Description=Boxy Sandbox Orchestrator
After=network.target

[Service]
Type=simple
User=boxy
Group=boxy
WorkingDirectory=/opt/boxy              # Config at /opt/boxy/boxy.yaml ✅
ExecStart=/usr/local/bin/boxy serve
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

---

## Documentation Updates

### Update Getting Started

**Old:**
```markdown
1. Create config at `~/.config/boxy/boxy.yaml`
2. Run `boxy serve`
```

**New:**
```markdown
1. Create project directory
   ```bash
   mkdir /opt/boxy && cd /opt/boxy
   ```

2. Create config file
   ```bash
   cat > boxy.yaml <<EOF
   storage:
     type: sqlite
     path: ./data/boxy.db
   # ...
   EOF
   ```

3. Run server
   ```bash
   boxy serve  # Uses ./boxy.yaml
   ```
```

### Update QUICKSTART.md

```markdown
## Quick Start

```bash
# 1. Create project directory
mkdir my-boxy-project && cd my-boxy-project

# 2. Initialize config
boxy init

# 3. Edit config
vim boxy.yaml

# 4. Start server
boxy serve
```
```

---

## Testing

### Integration Tests

```go
// tests/integration/config_test.go
func TestConfigLoading_CurrentDirectory(t *testing.T) {
    // Create temp directory
    tmpDir := t.TempDir()
    os.Chdir(tmpDir)

    // Create config in current directory
    configContent := `
storage:
  type: sqlite
  path: ./data/boxy.db
logging:
  level: info
`
    os.WriteFile("boxy.yaml", []byte(configContent), 0644)

    // Load config
    config, err := config.LoadConfig("")
    assert.NoError(t, err)
    assert.Equal(t, "./data/boxy.db", config.Storage.Path)
}

func TestConfigLoading_ExplicitPath(t *testing.T) {
    tmpDir := t.TempDir()
    configPath := filepath.Join(tmpDir, "custom-config.yaml")

    // Create config at custom location
    configContent := `...`
    os.WriteFile(configPath, []byte(configContent), 0644)

    // Load with explicit path
    config, err := config.LoadConfig(configPath)
    assert.NoError(t, err)
}

func TestConfigLoading_NotFound(t *testing.T) {
    tmpDir := t.TempDir()
    os.Chdir(tmpDir)

    // No config file exists
    _, err := config.LoadConfig("")
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "config file not found")
}
```

---

## Success Criteria

- ✅ Config loaded from `./boxy.yaml` by default
- ✅ `--config` flag overrides default
- ✅ Database path resolved relative to config file
- ✅ Error message clear when config not found
- ✅ Docker deployment works with mounted config
- ✅ Systemd service works with WorkingDirectory
- ✅ All examples updated
- ✅ Documentation updated
- ✅ Migration guide provided

---

## User Impact

### Breaking Change

**Old behavior:**
```bash
boxy serve    # Looked for ~/.config/boxy/boxy.yaml
```

**New behavior:**
```bash
boxy serve    # Looks for ./boxy.yaml (current directory)
```

### Migration Required

Users must:
1. Copy config from `~/.config/boxy/` to project directory
2. Update paths to be project-relative
3. Update deployment scripts (systemd, Docker)

See [migration-guide.md](migration-guide.md) for detailed steps.

---

## Related Documents

- [12: Docker & Compose](12-docker-compose.md) - Container deployment
- [migration-guide.md](migration-guide.md) - Breaking changes guide
- [08: Config Schema](08-config-schema.md) - YAML schema

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: None
**Blocking**: None (improves deployment but not required for other features)
