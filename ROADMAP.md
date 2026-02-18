# Boxy Roadmap

Upcoming design work and implementation plans.

---

## Agent Registration and Auth

### Token Bootstrap Flow (GitLab Runner model)

Agents authenticate using a two-phase token bootstrap. A shared registration token is used once to establish a unique per-agent identity, then discarded.

**Phase 1 — Registration:**

1. Admin generates a registration token (single-use or time-limited):
   ```
   boxy agent token create
   → bxy_reg_abc123...
   ```

2. Agent registers (first time only):
   ```
   boxy agent --server boxy-server:9090 --register-token bxy_reg_abc123
   ```
   - Agent presents registration token over gRPC/TLS
   - Server validates token, issues a unique per-agent JWT
   - Registration token is burned (single-use)
   - Agent stores its unique token locally

**Phase 2 — Ongoing:**

3. Future connections use the agent token automatically:
   ```
   boxy agent --server boxy-server:9090
   ```
   Agent reads its stored token, connects to the server, and begins accepting work.

4. Periodic key rotation: agent and server negotiate new tokens over the gRPC stream. On each rotation the previous token is invalidated, so leaked tokens go stale quickly.

### Agent Management CLI

```
boxy agent token create      — generate a new registration token
boxy agent list              — show connected/known agents and their capabilities
boxy agent revoke <id>       — revoke a specific agent's token
```

### Security Properties

- **Single-use registration tokens** — limits the exposure window. A leaked registration token can only be used once; after that it's burned.
- **Unique per-agent identity** — the server tracks each agent individually. A compromised agent can be revoked without affecting others.
- **Periodic key rotation** — agent and server rotate tokens over the gRPC stream. Even if an agent token is captured, it becomes invalid after the next rotation.
- **Config-declared agents** — remote agents are declared in the `agents:` section of `boxy.yaml`. The server knows what to expect and can alert on missing agents. The registration token authenticates the agent on first connect; the config declaration establishes the expected topology.

### Token Format

JWTs signed by the server's signing key. Claims:

```json
// Registration token
{
  "type": "registration",
  "exp": 1700000000
}

// Agent token (issued after registration)
{
  "type": "agent",
  "agent_id": "ag_xyz",
  "iat": 1700000000,
  "exp": 1700086400
}
```

The server validates tokens statelessly via signature verification. Revocation is handled by maintaining a deny-list in the state store (bbolt), checked on connection establishment.

### Transport

- gRPC over TLS for all agent-server communication
- Agent initiates the connection (NAT/firewall friendly)
- Bidirectional streaming: server pushes work, agent reports results
- Token is sent as gRPC metadata on stream establishment
- Rotation happens as an in-band RPC on the existing stream

### Dependencies

- `github.com/golang-jwt/jwt/v5` — token signing and validation
- `google.golang.org/grpc` — agent-server transport (already in use)
- `crypto/tls` (stdlib) — transport security
