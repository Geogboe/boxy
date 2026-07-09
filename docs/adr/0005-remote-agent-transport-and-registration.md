# ADR 0005: Remote Agent Transport and Registration

Status: Accepted

## Context

Boxy's only agent implementation today is `agentsdk.EmbeddedAgent`, running
in-process inside `boxy serve` and hardcoded as the single `Agent` field on
`internal/pool.AgentProvisioner`. This blocks the driving use case for this
release: a central boxy server (e.g. Kubernetes-hosted) that needs to
schedule provisioning against remote capacity — most immediately, a Windows
Hyper-V host — that cannot be dialed into directly (NAT/firewall). The agent
must dial *out* to the server instead (#37), and connecting agents must
authenticate and be revocable (#62).

This is greenfield work. Confirmed directly against `go.mod`: zero
gRPC/protobuf dependencies exist in this repo today. Confirmed directly
against `internal/server/`: zero authentication of any kind exists anywhere
in the codebase today. A prior pull-model `Config.Agents`/`AgentSpec` field
(server dials a statically-configured agent address) was dead code, never
read anywhere, and has already been removed (#36) — it is not a partial
implementation to extend, and the design below does not resemble it.

## Decision

### Transport: gRPC bidirectional streaming, agent-initiated

The agent process dials out to the server and opens one long-lived
bidirectional gRPC stream (`rpc Connect(stream AgentMessage) returns (stream
ServerMessage)`, see `proto/boxyagent/v1/agent.proto`). The agent sends a
`RegisterRequest` (first frame only), then interleaves `Heartbeat`s and
`CommandResult`s. The server sends a `RegisterResponse` (ack) and pushes
`Command`s down the same stream. One RPC carries both directions, avoiding
the need to correlate two independent streams/RPCs at the connection level.

`providersdk.Driver.Create`'s `cfg any`, `Operation interface{}`, and
`Driver.Allocate`'s `map[string]any` result all cross the wire as opaque
JSON bytes (`config_json`/`operation_json`/`properties_json`) rather than
typed proto messages or protobuf maps — modeling them per-driver in
protobuf would couple the wire schema to every current and future driver,
breaking the existing "opaque config blob interpreted only by the driver"
design, and a protobuf map requires one uniform value type, which
`map[string]any` does not guarantee.

`google.golang.org/grpc` and `google.golang.org/protobuf` are added as direct
dependencies of the main module. Protobuf/gRPC code generation uses `buf`
(not raw `protoc`), pinned in the isolated `tools/go.mod` module the same way
GoReleaser is pinned today (a `tool` directive, invoked via `go run
-modfile=tools/go.mod ...`, never polluting the main module's `go.mod`).
Generated Go code under `pkg/agentproto/` is committed to git, so `go
build`/`go test` never require `buf` to be installed — only contributors
editing the `.proto` schema need it.

### TLS: private CA, full mTLS

Boxy mints and owns its own private CA — not a public CA like Let's
Encrypt, which is a separate, out-of-scope concern reserved for the web
dashboard. The gRPC listener presents a leaf certificate signed by this CA;
agents authenticate back via a client certificate, also signed by the same
CA, issued at registration time (full mutual TLS). This is the standard
pattern for internal agent-fleet traffic (e.g. Kubernetes kubelet↔apiserver,
Consul/Nomad agent RPC) as opposed to public-CA-issued certificates, which
exist to let arbitrary browsers trust a server without prior configuration —
not the problem this transport has.

CA and server cert material live under the `.boxy/` directory convention
already established by `.boxy/state.json`: `.boxy/ca.crt`, `.boxy/ca.key`,
`.boxy/server.crt`, `.boxy/server.key`.

`--insecure`/`--dev` (skip strict mTLS for local development) is a **CLI
flag only** — deliberately not exposed in `boxy.yaml`. A config field would
risk a stale or copy-pasted config file silently disabling mTLS in a real
deployment; a flag must be passed explicitly on every invocation.

### Registration: GitLab-Runner-style two-phase bootstrap

An operator mints a single-use, short-lived registration token via the
server's existing REST API (`boxy agent token create`). `boxy agent serve
--server <url> --providers <list> --token <token>` redeems that token
exactly once, over the initial gRPC stream; the server marks it used
immediately (before issuing anything, so it can never be redeemed twice
even under concurrent misuse) and issues a client certificate for every
subsequent reconnect. Reconnects present that certificate instead of a
token; the server checks it isn't in the revocation deny-list before
re-registering.

### Multi-agent routing and per-resource agent provenance

`internal/pool.AgentProvisioner`'s single hardcoded `Agent` field becomes a
`Registry` (`internal/pool/agent_registry.go`) that can hold the embedded
agent plus any number of connected remote agents, resolved by provider type
with an optional explicit pin (`PoolSpec.Agent`).

A correctness issue was found and fixed during design review: `Destroy` and
`Allocate` operate on an *existing* resource and must route to the exact
agent instance that created it — not re-resolve by provider type at call
time, which `Provision` does for a *new* resource. Once two agents can
advertise the same provider type (e.g. two independent Hyper-V hosts both
offering `hyperv`), type-based resolution could route a `Destroy` call to
the wrong agent. Because `providersdk.Driver.Delete` is contractually
idempotent for an already-missing resource, a misrouted `Destroy` would
report success silently while the resource keeps running, unmanaged, on its
real host. `pkg/model.ProviderRef` gains an `AgentID` field, stamped at
`Provision` time with the resolving agent's ID; `Destroy`/`Allocate` use a
new `Registry.Get(agentID)` exact-instance lookup instead, failing loudly
(and retriably, via the pool manager's existing backoff path) if that
specific agent is unavailable, rather than silently substituting a
different one.

### Token/revocation storage: extend the existing Store, no bbolt

`pkg/store.Store` (and both its `DiskStore`/`MemoryStore` implementations)
gain methods for registration tokens and revoked agent identities. No new
persistence engine (e.g. bbolt) is introduced. This mirrors `DiskStore`'s own
stated rationale for existing (avoid a new dependency until state genuinely
outgrows a single JSON file); token/revocation counts are expected to stay
in the 10s-100s, well within what full-JSON-rewrite-on-write already handles
for pools/resources/sandboxes.

### Heartbeat and unavailability

The agent sends a `Heartbeat` on the open stream at a configurable interval
(`server.agent_heartbeat_interval`, default 15s — close to the existing 10s
reconcile tick). After N missed heartbeats, the registry marks that agent's
providers unavailable for **new** provisioning only; already-allocated
resources are never force-torn-down. This mirrors ADR-0004's Hyper-V
"never force a destructive operation against something we can't confirm is
safe to touch" precedent, and — like ADR-0004's provisioning backoff — this
state is in-memory only and resets on daemon restart.

## Consequences

- Every existing single-agent config is unaffected: the embedded agent has
  a fixed, well-known ID (`"embedded"`), so the new `ProviderRef.AgentID`
  field is stamped identically on every resource today, and `agent:` pinning
  is optional.
- **In-flight command loss on stream drop is accepted, not solved, in this
  pass.** If a `Create` is sent and the stream drops before its
  `CommandResult` returns, the remote side may have actually succeeded —
  Boxy has no way to know, and (unlike `Delete`) `Create` is not
  idempotent by contract. A dropped-but-successful `Create` can leak an
  unmanaged resource. Tracked as a follow-up issue for a post-reconnect,
  `Read`-based reconciliation sweep — not blocking this work.
- **No resource-state reconciliation after an agent reconnects**, for the
  same reason: the embedded-agent model never had network partitions to
  account for. Follow-up issue, not blocking.
- **Heartbeat-derived unavailability resets on daemon restart** — the same
  accepted tradeoff ADR-0004 already made for its own backoff state.
- **Agent identity churn can orphan resources.** If a remote host is rebuilt
  and re-registers under a *new* agent ID rather than reconnecting with its
  existing certificate, resources stamped with the old `AgentID` become
  permanently un-`Destroy`able through the normal path — by design, since
  the system deliberately refuses to guess a substitute agent. This is the
  correct failure mode (loud and safe, not silent and wrong), but it means
  an operator needs a manual escape hatch for "the whole remote host is
  gone for good." Not solved in this pass.
- Revoking a connected agent (`boxy agent revoke <id>`) must actively tear
  down its live stream, not just remove it from the in-memory registry —
  otherwise a revoked-but-still-connected agent keeps serving commands
  until it happens to disconnect on its own.

## Alternatives Considered

1. **Raw HTTP/2 with a hand-rolled streaming protocol.** Rejected: avoids
   the protobuf/buf toolchain, but requires hand-building frame boundaries,
   message correlation, and backpressure — real risk of subtle networking
   bugs for a first-time implementation of this shape, versus reusing
   HTTP/2 flow control and framing that gRPC already provides.
2. **WebSockets (`coder/websocket` or `gorilla/websocket`).** A credible,
   lighter-weight alternative — TLS reuses the same stdlib mechanism as the
   REST API, no codegen step. Rejected in favor of gRPC because this exact
   "agent dials home, bidirectional, typed, secure" shape is the
   well-precedented industry pattern (Kubernetes CRI/CSI, HashiCorp
   `go-plugin`, Teleport's reverse tunnel), and `grpc-go` is a CNCF
   project with the security-patch cadence that matters for a
   security-relevant transport; `gorilla/websocket` in particular has had
   no release in two years, a legitimate concern for this use case.
3. **Server-Sent Events (push) + plain REST POST (agent-initiated),
   stdlib-only, zero new dependencies.** Genuinely the lowest-trust-surface
   option, reusing this repo's own existing REST client/server patterns.
   Rejected as needlessly custom: it requires hand-rolling command/response
   correlation and a heartbeat convention across two independent HTTP flows
   for a problem gRPC's bidirectional streaming already solves as one
   primitive.
4. **Connect (`connectrpc.com/connect`).** A lighter-weight gRPC-adjacent
   framework (built on `net/http`, can also speak plain HTTP/JSON). Still
   requires the same `.proto`/`buf` codegen toolchain as gRPC — it reduces
   runtime footprint, not the toolchain complexity that was the actual
   concern — so it wasn't a meaningfully different choice for this decision.
5. **bbolt for token/revocation storage**, per this feature's originating
   issue's own assumption. Rejected: introduces a second persistence engine
   alongside `DiskStore` for a collection expected to stay in the
   10s-100s, when `DiskStore`'s own existing rationale is explicitly to
   avoid exactly this kind of premature dependency addition.
