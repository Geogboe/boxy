# ADR-003: Configuration and State Storage

**Date**: 2025-11-17
**Status**: Accepted

## Context

Boxy needs to:
1. Store pool configuration (what pools exist, their settings)
2. Store runtime state (allocated resources, sandbox state)
3. Support easy development/testing
4. Be production-ready eventually

## Decision

### Phase 1 (MVP): File-based Configuration + SQLite State

**Pool Configuration**: YAML config files
```yaml
# boxy.yaml
pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 3
    max_total: 10

  - name: win-server-vms
    type: vm
    backend: hyperv
    image: "Windows Server 2022"
    min_ready: 1
    max_total: 5
```

**Runtime State**: SQLite database
- Resource allocations
- Sandbox state
- Credential storage (encrypted)
- Audit logs

### Phase 3+: Database-backed Configuration

Migrate pool configuration to database when API server is added, enabling:
- Dynamic pool creation via API
- Pool modification without restarts
- Multi-node coordination

## Rationale

### Why Config Files for MVP?

1. **Simple**: No database setup for pool definitions
2. **Version Control**: Pool configs can be committed to Git
3. **GitOps-Friendly**: Infrastructure-as-code approach
4. **Declarative**: Easy to understand and review
5. **No API Required**: CLI can read files directly

### Why SQLite for State?

1. **Zero Configuration**: Single file, no server setup
2. **Perfect for Development**: Easy testing and debugging
3. **ACID Guarantees**: Reliable state management
4. **Migration Path**: GORM allows seamless PostgreSQL migration
5. **Embedded**: No external dependencies

### Why YAML over TOML/JSON?

1. **Comments**: Documentation inline with config
2. **Readability**: Less verbose than JSON
3. **Industry Standard**: Used by Kubernetes, Docker Compose, GitHub Actions
4. **Ecosystem**: Excellent Go library (`gopkg.in/yaml.v3`)

### Why Not PostgreSQL for MVP?

- Requires separate process/container
- Adds deployment complexity
- Overkill for single-node MVP
- We can migrate later with zero code changes (GORM abstraction)

## Consequences

### Positive
- **Simple Development**: No database setup needed
- **Easy Testing**: SQLite in-memory mode for tests
- **Version Control**: Config files in Git
- **Fast Iteration**: Edit YAML, restart, done

### Negative
- **No Dynamic Pools**: Can't create pools via API (Phase 1)
- **Restart Required**: Config changes need restart
- **Single Node**: SQLite limits scaling (but MVP is single-node anyway)

### Migration Path

**Phase 3 Migration**:
1. Add database tables for pool configuration
2. Create migration tool: `boxy migrate config-to-db`
3. Support both config file and database (config file takes precedence)
4. Eventually deprecate config file for pools (Phase 4+)

**PostgreSQL Migration**:
```go
// Same code works with both!
db, err := gorm.Open(sqlite.Open("boxy.db"), &gorm.Config{})
// vs
db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
```

## Configuration File Location

**Search Order**:
1. `--config` flag
2. `./boxy.yaml`
3. `~/.config/boxy/boxy.yaml`
4. `/etc/boxy/boxy.yaml`

Implemented via `viper` library.

## State Database Location

**Default**: `~/.config/boxy/boxy.db`
**Override**: `--db` flag or `BOXY_DB_PATH` environment variable

## Security Considerations

### Configuration Files
- Should NOT contain sensitive credentials
- Provider-specific credentials via environment variables or external secret stores

### State Database
- Encrypt sensitive fields (resource credentials) at rest
- Use `golang.org/x/crypto` for encryption
- Per-resource random credentials
- Cleanup credentials on resource destruction

## Alternatives Considered

### 1. Database-Only Configuration
**Rejected for MVP**: Requires API server, more complex, harder to version control.
**Future**: Migrate in Phase 3 when API server is added.

### 2. JSON Configuration
**Rejected**: No comments, more verbose, less readable.

### 3. TOML Configuration
**Rejected**: Less common in cloud-native tools, no significant advantages.

### 4. PostgreSQL from Day 1
**Rejected**: Unnecessary complexity for MVP, easy migration path available.

## References
- [Tech Stack Research - State Storage](../architecture/tech-stack-research.md#database-options-analysis)
- [ROADMAP - Phase 1 Scope](../ROADMAP.md#phase-1-mvp---single-backend-docker)
