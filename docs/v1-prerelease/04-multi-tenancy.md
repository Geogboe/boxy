# 04: Multi-Tenancy

## History

```yaml
Origin: "docs/v1-prerelease/04-multi-tenancy.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Migrated to planning; ADR-010 drafted and linked."
```

> Status: proposed — See `docs/decisions/adr-010-multi-tenancy.md` for ADR.

---

## Metadata

```yaml
feature: "Multi-Tenancy"
slug: "multi-tenancy"
status: "not-started"
priority: "high"
type: "feature"
effort: "large"
depends_on: ["architecture-refactor"]
enables: ["production-readiness", "user-isolation"]
testing: ["unit", "integration", "e2e"]
breaking_change: true
week: "3-4"
related_docs:
  - "migration-guide.md"
```

---

## Overview

Add users, teams, API tokens, quotas, and ownership tracking. Required for production deployment.

**Key features:**

- User accounts with roles (admin, user)
- API token authentication
- Sandbox ownership (user_id, team_id)
- Basic quotas (max concurrent sandboxes)
- Authorization (users see only their sandboxes)

---

## Database Schema

```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT,
    api_token TEXT UNIQUE NOT NULL,
    role TEXT NOT NULL,  -- 'admin' or 'user'
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_users_token ON users(api_token);
CREATE INDEX idx_users_username ON users(username);

-- Update sandboxes table
ALTER TABLE sandboxes ADD COLUMN user_id TEXT;
ALTER TABLE sandboxes ADD COLUMN team_id TEXT;

CREATE INDEX idx_sandboxes_user_id ON sandboxes(user_id);
CREATE INDEX idx_sandboxes_team_id ON sandboxes(team_id);
```

---

## API Token Generation

```go
func generateAPIToken() string {
    bytes := make([]byte, 32)
    if _, err := cryptoRand.Read(bytes); err != nil {
        panic(err)
    }
    return "bxy_" + base64.URLEncoding.EncodeToString(bytes)[:40]
}
```

---

## Authentication Middleware

```go
func AuthMiddleware(userRepo user.UserRepository) gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("Authorization")
        token = strings.TrimPrefix(token, "Bearer ")

        user, err := userRepo.GetUserByToken(c.Request.Context(), token)
        if err != nil {
            c.JSON(401, gin.H{"error": "invalid API token"})
            c.Abort()
            return
        }

        c.Set("user", user)
        c.Next()
    }
}
```

---

## CLI Commands

```bash
# Admin creates users
boxy admin create-user --username alice --role user
# Output: API Token: bxy_abc123xyz789

# User configures CLI
# ~/.config/boxy/cli-config.yaml
server: https://boxy-server:8443
token: bxy_abc123xyz789

# All commands now authenticated
boxy sandbox create -p pool:1 -d 1h
```

---

## Success Criteria

- ✅ User model implemented
- ✅ API token generation secure (crypto/rand)
- ✅ Authentication middleware works
- ✅ Sandbox ownership tracked
- ✅ Users see only their sandboxes
- ✅ Basic quotas enforced
- ✅ Admin user created on migrate
- ✅ Migration guide updated

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: 01-architecture-refactor
**Blocking**: None
