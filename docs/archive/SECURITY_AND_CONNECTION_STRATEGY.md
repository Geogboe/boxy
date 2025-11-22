# Security and Connection Strategy Analysis

## Current Security Issues ⚠️

### 1. **Insecure by Default** (CRITICAL)
```go
// Current: Defaults to insecure!
UseTLS: false  // User must explicitly enable TLS
```

**Problem**: Agents expose privileged operations (provision VMs, exec commands). Running without TLS means:
- No encryption → credentials visible on network
- No authentication → anyone can connect
- No integrity → MITM attacks possible

**Fix**: Default to TLS required, allow insecure only for dev with explicit flag

### 2. **No Mutual Authentication**
Current TLS only validates server certificate. Agent doesn't verify client identity.

**Risk**: Any client with network access can call agent APIs.

**Fix**: Use mTLS (mutual TLS) where both sides present certificates

### 3. **Credentials in Logs**
Current code logs full requests/responses which may contain passwords.

**Fix**: Sanitize logs, never log password fields

### 4. **No Rate Limiting**
Agent accepts unlimited requests from any connected client.

**Risk**: DoS attacks, resource exhaustion

**Fix**: Add rate limiting per connection

### 5. **No Input Validation**
Proto messages accepted without validation.

**Risk**: Malformed data could crash agent or exploit bugs

**Fix**: Validate all inputs before processing

## Connection Strategy Analysis

### Current Implementation: Long-Running gRPC Connection

**How it works**:
```
Server (Linux) ─────gRPC connection────▶ Agent (Windows)
                     ↓
              Provision/Destroy/Exec RPCs
```

**Characteristics**:
- Server initiates connection to agent
- Connection stays open
- RPCs sent over same connection
- Client-side retry on failure

### Industry Standards

#### 1. **Kubernetes Model** (kubelet → API server)
- **Pattern**: Agent connects to control plane
- **Connection**: Long-lived gRPC with bidirectional streaming
- **Health**: API server tracks agent heartbeats
- **Reconnection**: Automatic with exponential backoff
- **Used by**: Kubernetes, K3s, K0s

#### 2. **HashiCorp Nomad/Consul**
- **Pattern**: Gossip protocol + RPC
- **Connection**: TCP with multiplexing (Yamux)
- **Health**: Serf gossip protocol for failure detection
- **Reconnection**: Built into gossip protocol
- **Used by**: Nomad, Consul, Vault

#### 3. **gRPC Standard Pattern**
- **Pattern**: Client → Server RPC
- **Connection**: HTTP/2 with multiplexing
- **Health**: grpc.health.v1.Health service
- **Reconnection**: Client retry with backoff
- **Keepalive**: Built-in TCP keepalive + HTTP/2 PING frames

### Our Implementation: Which Pattern?

**Current**: Closest to **gRPC Standard Pattern**
- Server calls agent (client → server RPC)
- Simple, uses gRPC built-ins
- Good for MVP, scalable to 10s of agents

**Problem**: Direction is backwards!

Correct pattern for security:
```
Agent (untrusted network) ──connects to──▶ Server (trusted network)
```

Not:
```
Server (trusted) ──connects to──▶ Agent (untrusted) ❌
```

**Why?** Firewalls, NAT, security policies usually block inbound connections to agents.

## Recommended Architecture

### Option A: Reverse Tunnel (Most Secure)

**Pattern**: Agent opens tunnel to server, server sends RPCs back through tunnel

```
Agent (Windows, firewall) ──TLS tunnel──▶ Server (Linux)
                                            │
                                            ▼
                        RPCs flow back through same tunnel
```

**Pros**:
- Works through firewalls/NAT
- Agent initiates connection (security best practice)
- Server doesn't need agent network access
- Agent can be behind corporate firewall

**Cons**:
- More complex implementation
- Need bidirectional streaming

**Frameworks**:
- **Ngrok/Chisel pattern**: Reverse tunnel
- **gRPC bidirectional streaming**: Built-in
- **NATS**: Message queue with request-reply

### Option B: Pull-Based (Simplest)

**Pattern**: Server puts tasks in queue, agents poll and execute

```
Server ──▶ Task Queue (Redis/DB)
              ▲
              │ (poll every 5s)
              │
           Agent
```

**Pros**:
- Agent initiates all connections
- Simple to implement
- Works through firewalls
- Easy to add multiple agents

**Cons**:
- Higher latency (polling interval)
- More database load

**Used by**: Jenkins agents, GitHub Actions runners

### Option C: Hybrid - Long Polling + Keepalive (Recommended)

**Pattern**: Agent connects, streams requests, server pushes RPCs

```go
// Agent calls server
stream = server.StreamWork(ctx)
for {
    task := stream.Recv()  // Blocks until work available
    result := executeTask(task)
    stream.Send(result)
}
```

**Pros**:
- Low latency (push-based)
- Agent initiates connection
- Works through firewalls
- Built into gRPC

**Cons**:
- Need bidirectional streaming

**Used by**: Kubernetes watch API, etcd watch

## Recommended Solution: gRPC Keepalive + Reverse RPC

**Step 1**: Agent establishes bidirectional stream to server
**Step 2**: Server registers agent as available
**Step 3**: When work needed, server sends RPC over stream
**Step 4**: Agent executes and responds

**Code pattern**:
```protobuf
service AgentService {
  // Agent calls this to stream work
  rpc StreamWork(stream WorkRequest) returns (stream WorkResponse);
}
```

**This is already designed in your proto!** The AgentService with Register/Heartbeat.

## Efficiency Analysis

### Current Implementation

**gRPC Connection**:
- HTTP/2 multiplexing: ✅ Efficient (multiple RPCs on one TCP connection)
- Connection pooling: ❌ Not implemented (creates new connection per RemoteProvider)
- Keepalive: ❌ Not configured (connections may die silently)
- Health monitoring: ⚠️ RPC exists but not used proactively

**Issues**:
1. **No keepalive**: Connection may timeout after idle period
2. **No connection reuse**: Each pool creates new connection to same agent
3. **No health probing**: Don't know if agent is down until RPC fails

### Fixes Needed

#### 1. Enable gRPC Keepalive (Standard Practice)

```go
// In RemoteProvider
opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
    Time:                10 * time.Second,  // Send ping every 10s
    Timeout:             5 * time.Second,   // Wait 5s for ping response
    PermitWithoutStream: true,              // Ping even without active RPCs
}))
```

#### 2. Connection Pooling

```go
// Singleton connection per agent
type AgentConnectionPool struct {
    mu    sync.RWMutex
    conns map[string]*grpc.ClientConn  // agent address → connection
}
```

#### 3. Health Monitoring

```go
// Periodic health check from server
go func() {
    ticker := time.NewTicker(30 * time.Second)
    for range ticker.C {
        if err := remoteProvider.HealthCheck(ctx); err != nil {
            // Mark agent as unhealthy, retry or alert
        }
    }
}()
```

## Recommended Framework/Package

**Answer: gRPC is the framework, it's already correct!**

But we should add:
1. **gRPC Keepalive**: Built-in, just need to enable
2. **gRPC Health Checking**: Standard protocol (grpc.health.v1)
3. **Connection pooling**: Simple to implement
4. **Exponential backoff**: Already implemented ✅

**Don't need external framework** - gRPC provides everything.

## Action Plan

### Phase 1: Fix Current Implementation (2 hours)
1. ✅ Fix compilation errors
2. ✅ Enable gRPC keepalive (5 lines of code)
3. ✅ Make TLS default (change UseTLS default)
4. ✅ Add connection pooling (prevent duplicate connections)
5. ✅ Test with mock agent

### Phase 2: Proper Security (4 hours)
1. Implement certificate generation commands
2. Enforce mTLS by default
3. Add rate limiting
4. Sanitize logs (don't log passwords)
5. Input validation

### Phase 3: Production-Ready (8 hours)
1. Implement agent registration (AgentService)
2. Add health monitoring from server side
3. Connection state tracking
4. Metrics and observability
5. Multi-agent load balancing

## For MVP: What's Reasonable?

**Keep**:
- gRPC (correct choice)
- Long-running connections (correct for gRPC)
- Client-side retry (already implemented)

**Add** (simple, high value):
1. gRPC keepalive (5 minutes to add)
2. TLS by default with --insecure flag for dev (10 minutes)
3. Connection pooling (30 minutes)
4. Log sanitization (15 minutes)

**Defer** (complex, lower priority for MVP):
- Agent registration service
- mTLS with full cert management
- Rate limiting
- Connection state tracking

**Total MVP security hardening: ~1 hour**

## Conclusion

**Current implementation is 80% correct**:
- ✅ gRPC is the right choice
- ✅ Connection strategy is standard
- ✅ Retry logic implemented
- ❌ Missing keepalive (easy fix)
- ❌ Insecure by default (easy fix)
- ❌ No connection pooling (easy fix)

**No external framework needed** - gRPC is the framework.

**Next steps**: Let me implement the 1-hour security hardening while fixing compilation.
