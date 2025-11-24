# Migration Guide - v1 Prerelease

> ARCHIVED: This planning document has been moved to `docs/planning/v1-prerelease/migration-guide.md`. Please edit that file for proposals. The original content is preserved here for historical reference.

## History

```yaml
Origin: "docs/v1-prerelease/migration-guide.md"
SourceType: "migration-notes"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Moved to `docs/planning/v1-prerelease/migration-guide.md` for planning centralization."
```

---

## Metadata

```yaml
feature: "Migration Guide"
slug: "migration-guide"
status: "not-started"
priority: "high"
type: "documentation"
effort: "small"
depends_on: ["all-v1-features"]
enables: ["smooth-upgrade"]
testing: ["manual"]
breaking_change: false
week: "9"
```

---

## Overview

This guide covers migration to v1-prerelease. Since v1 is **pre-release** and no production deployments exist, **breaking changes are acceptable**.

---

## Breaking Changes Summary

| Change | Impact | Migration Required |
| -------- | -------- | ------------------- |
| Hook names changed | Config | Update YAML |
| New resource states | Code | Automatic migration |
| Multi-tenancy | Auth | Create users |
| Config location | Deployment | Move config file |
| Database schema | Data | Run migrations |

---

## Configuration Changes

### Hook Names

**Before:**

```yaml
pools:
  - name: ubuntu-containers
    hooks:
      after_provision:       # OLD
        - type: script
          inline: echo "provisioned"

      before_allocate:       # OLD
        - type: script
          inline: echo "allocating"
```

**After:**

```yaml
pools:
  - name: ubuntu-containers
    hooks:
      on_provision:          # NEW
        - type: script
          inline: echo "provisioned"

      on_allocate:           # NEW
        - type: script
          inline: echo "allocating"
```

**Backwards Compatibility:** OLD names still work with deprecation warning (for now).

### Preheating Configuration

**New:**

```yaml
pools:
  - name: ubuntu-containers
    min_ready: 10
    max_total: 20

    preheating:              # NEW
      enabled: true
      count: 3               # Keep 3 warm
      recycle_interval: 1h
      recycle_strategy: rolling
      warmup_timeout: 5m
```

**Default:** If omitted, preheating is disabled (all resources cold).

### Distributed Agents

**New:**

```yaml
server:
  mode: server
  listen_address: 0.0.0.0:8443
  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca-cert.pem
    client_auth: require

agents:
  - id: windows-host-01
    address: windows-host-01:8444
    providers: [hyperv]
    tls:
      cert_file: /etc/boxy/agents/windows-01-cert.pem
      key_file: /etc/boxy/agents/windows-01-key.pem
      ca_file: /etc/boxy/ca-cert.pem

pools:
  - name: win-vms
    backend: hyperv
    backend_agent: windows-host-01  # NEW: Route to agent
```

---

## Database Migration

### Schema Changes

```sql
-- Add user tracking to sandboxes
ALTER TABLE sandboxes ADD COLUMN user_id TEXT;
ALTER TABLE sandboxes ADD COLUMN team_id TEXT;

CREATE INDEX idx_sandboxes_user_id ON sandboxes(user_id);
CREATE INDEX idx_sandboxes_team_id ON sandboxes(team_id);

-- Create users table
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT,
    api_token TEXT UNIQUE NOT NULL,
    role TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_users_token ON users(api_token);
CREATE INDEX idx_users_username ON users(username);

-- Create default admin user
INSERT INTO users (id, username, email, api_token, role, created_at, updated_at)
VALUES (
    'user-admin',
    'admin',
    'admin@localhost',
    '<generated-token>',
    'admin',
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
);

-- Backfill existing sandboxes with admin user
UPDATE sandboxes SET user_id = 'user-admin' WHERE user_id IS NULL;
```

### Migration Command

```bash
# Run automatic migration
boxy admin migrate

# Output:
# ✅ Added users table
# ✅ Added user_id/team_id to sandboxes
# ✅ Created default admin user
# ✅ Backfilled existing sandboxes
#
# Default admin credentials:
#   Username: admin
#   API Token: bxy_<generated>
#
# IMPORTANT: Save this token, it will not be shown again!
```

### Manual Migration (if needed)

```bash
# Export current state
boxy admin export --output backup.json

# Upgrade Boxy
# ... install new version ...

# Run migration
boxy admin migrate

# Verify
boxy sandbox ls  # Should show all sandboxes owned by admin
```

---

## Config File Location

### Old Location

```text
~/.config/boxy/boxy.yaml    # User-specific (WRONG for service)
```

### New Location

```text
./boxy.yaml                 # Project directory (CORRECT)
```

**Rationale**: Boxy is a service orchestrator, not a user CLI tool. Config should be project-specific.

### Migration Steps

```bash
# 1. Copy config to new location
cp ~/.config/boxy/boxy.yaml ./boxy.yaml

# 2. Update systemd service (if using)
# /etc/systemd/system/boxy.service
[Service]
WorkingDirectory=/opt/boxy    # Config in /opt/boxy/boxy.yaml
ExecStart=/usr/local/bin/boxy serve

# 3. Restart service
systemctl daemon-reload
systemctl restart boxy
```

---

## Authentication

### API Tokens Required

**Before:** No authentication (pre-v1)

**After:** All API/CLI calls require authentication

### Setup

```bash
# 1. Migrate database (creates admin user)
boxy admin migrate

# 2. Create additional users
boxy admin create-user --username alice --role user

# Output: API Token: bxy_<generated>

# 3. Configure CLI
# ~/.config/boxy/cli-config.yaml
server: https://boxy-server:8443
token: bxy_<your-token>
```

### API Usage

```bash
# Before (no auth)
curl http://localhost:8080/api/v1/sandboxes

# After (with auth)
curl -H "Authorization: Bearer bxy_<token>" \
     https://localhost:8443/api/v1/sandboxes
```

---

## Code Changes (Internal)

**If you have custom integrations or plugins:**

### Pool Interface

**Before:**

```go
// Sandbox directly called pool.Allocate()
pool, _ := pools["ubuntu-containers"]
res, err := pool.Allocate(ctx, sandboxID)
```

**After:**

```go
// Use Allocator
allocator := ... // Get from service
res, err := allocator.AllocateFromPool(ctx, "ubuntu-containers", sandboxID)
```

### Resource States

**New states added:**

```go
const (
    StateProvisioned  // Cold (stopped)
    StateWarming      // Starting up
    StateReady        // Warm (running)
    StateAllocating   // Being allocated
    StateAllocated    // In use
    StateRecycling    // Being recycled
    StateDestroyed    // Gone
    StateError        // Failed
)
```

**Handle new states in your code.**

---

## Deployment Changes

### Docker Compose

**New:** Boxy now supports Docker Compose deployment

```yaml
# docker-compose.yml
version: '3.8'
services:
  boxy-server:
    image: ghcr.io/geogboe/boxy:v1
    volumes:
      - ./boxy.yaml:/app/boxy.yaml
      - ./data:/app/data
    ports:
      - "8443:8443"
    environment:
      - BOXY_LOG_LEVEL=info

  boxy-agent-windows:
    image: ghcr.io/geogboe/boxy:v1-windows
    command: agent serve --providers hyperv
    # ... agent config
```

See [12-docker-compose.md](12-docker-compose.md) for details.

---

## Testing Your Migration

### Checklist

- [ ] Config file moved to new location
- [ ] Database migration completed successfully
- [ ] Default admin token saved securely
- [ ] CLI configured with API token
- [ ] Can list existing sandboxes: `boxy sandbox ls`
- [ ] Can create new sandbox: `boxy sandbox create -p pool:1 -d 1h`
- [ ] Can destroy sandbox: `boxy sandbox destroy <id>`
- [ ] Pool stats work: `boxy pool stats <pool-name>`
- [ ] Agents connected (if using distributed mode)
- [ ] No error logs in `boxy serve` output

### Smoke Test

```bash
# 1. Start server
boxy serve

# 2. Create sandbox
SANDBOX_ID=$(boxy sandbox create -p ubuntu-containers:1 -d 1h --json | jq -r '.id')

# 3. Wait for ready
boxy sandbox wait $SANDBOX_ID --timeout 30s

# 4. Get connection info
boxy sandbox resources $SANDBOX_ID

# 5. Destroy
boxy sandbox destroy $SANDBOX_ID

# If all steps succeed → migration successful ✅
```

---

## Rollback Plan

**If migration fails:**

### Step 1: Backup

**Before migrating, always backup:**

```bash
# Backup database
cp ~/.local/share/boxy/boxy.db ~/.local/share/boxy/boxy.db.backup

# Backup config
cp boxy.yaml boxy.yaml.backup

# Export state
boxy admin export --output state-backup.json
```

### Step 2: Rollback

```bash
# 1. Stop new version
systemctl stop boxy

# 2. Restore old binary
cp /usr/local/bin/boxy.backup /usr/local/bin/boxy

# 3. Restore database
cp ~/.local/share/boxy/boxy.db.backup ~/.local/share/boxy/boxy.db

# 4. Restore config
cp boxy.yaml.backup boxy.yaml

# 5. Restart
systemctl start boxy
```

---

## Common Issues

### Issue: "API token required"

**Cause**: Multi-tenancy now enforced

**Fix:**

```bash
# Get admin token from migration output
# OR generate new token
boxy admin reset-token --username admin
```

### Issue: "Config file not found"

**Cause**: Config not in current directory

**Fix:**

```bash
# Specify config explicitly
boxy serve --config /path/to/boxy.yaml

# OR copy to current directory
cp /old/path/boxy.yaml ./boxy.yaml
```

### Issue: "Unknown hook: after_provision"

**Cause**: Old hook names not supported

**Fix:** Update config to use new names (`on_provision`, `on_allocate`)

### Issue: "Pool has no ready resources"

**Cause**: Preheating disabled, resources are cold

**Fix:** Either:

1. Enable preheating in config
2. Wait for resources to warm on-demand

---

## Timeline

**Estimated migration time**: 10-30 minutes

- Config changes: 5 min
- Database migration: 1 min
- Testing: 10-20 min
- Troubleshooting (if needed): variable

---

## Support

**If you encounter issues:**

1. Check logs: `journalctl -u boxy -f`
2. Enable debug logging: `BOXY_LOG_LEVEL=debug boxy serve`
3. Review [10-debugging-guide.md](10-debugging-guide.md)
4. Open issue: <https://github.com/geogboe/boxy/issues>

---

**Last Updated**: 2025-11-23
**Applies To**: pre-v1 → v1-prerelease
**Breaking Changes**: Yes (acceptable for pre-release)
