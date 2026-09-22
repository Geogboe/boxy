# Overlay Network Fabric — JIT Native-Protocol Access (Plan 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an operator run `boxy connect <sandbox> --resource <id> --port <n>` and point a native client (`ssh`, `mstsc`) at a local port that tunnels straight to that one resource's port for the session's duration — no standing exposure, no client-side WireGuard, riding the same control-plane connections `boxy` already has open.

**Architecture:** A new `model.JITSession` (mirrors `model.Sandbox`'s persistence), reconciled by a new ticked loop (`internal/sandbox/jitreconciler.go`, mirroring `internal/sandbox/deleter.go`'s shape exactly) that owns every session's `ExpiresAt` — the agent never runs its own timer, only executes open/close commands, matching this project's control-plane-owns-timers convention (confirmed the hard way during this spec's design review). The data path is a full-duplex byte tunnel: `boxy connect` opens an HTTP request whose body it streams bytes into and whose response body it streams bytes out of, concurrently; `boxy serve` relays those bytes over a new bidirectional session channel multiplexed onto the *existing* single `AgentTransportService.Connect` stream (ADR-0005) to the target agent, which dials `resource:port` locally and pipes bytes back the same way. No new port, no new transport, no client-side WireGuard.

**Tech Stack:** Go 1.25, `net/http` (duplex streaming via concurrent read/flush, no `Hijack` needed — see Task 4's design note), protobuf/gRPC.

**Spec:** `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` — this plan implements Decision 3 in full. It does not implement Decision 4 (`AccessBroker`), which is the extension point this JIT relay sits behind as the default.

## Global Constraints

- The agent never owns a timer or makes an autonomous "close this session" decision — `boxy serve`'s reconciler is the only thing that decides a session has expired; the agent only ever executes an explicit close command it's told to run. Re-verify this in every task that touches session lifecycle.
- A `JITSession`'s dial target is validated against the sandbox's own `Resources` list server-side before any command is sent to an agent — a caller must not be able to ask an agent to dial an arbitrary resource ID that isn't part of the sandbox named in the URL.
- No data byte ever gets logged, in either direction. Log session metadata (session ID, resource ID, port, byte counts) freely; never log payload content.
- This plan does not implement `AccessBroker` (Decision 4) — a `JITSession` always uses this plan's own relay. The seam for `AccessBroker` to later intercept `boxy connect` instead is Plan 4's job, not this one's.

---

### Task 1: `model.JITSession`

**Files:**
- Create: `pkg/model/jit_session.go`
- Test: `pkg/model/jit_session_test.go`

**Interfaces:**
- Produces: `model.JITSessionID` (string), `model.JITSessionStatus` (`open`/`closed`/`failed`), `model.JITSession{ID, SandboxID, ResourceID, Port, Requester, ExpiresAt, Status, Error}`.

- [ ] **Step 1: Write the failing test**

```go
// pkg/model/jit_session_test.go
package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
)

func TestJITSession_RoundTripsThroughJSON(t *testing.T) {
	expires := time.Now().UTC().Truncate(time.Second)
	session := model.JITSession{
		ID:         "jit-1",
		SandboxID:  "sb-1",
		ResourceID: "res-1",
		Port:       22,
		Requester:  "alice",
		ExpiresAt:  expires,
		Status:     model.JITSessionStatusOpen,
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got model.JITSession
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "jit-1" || got.Port != 22 || !got.ExpiresAt.Equal(expires) || got.Status != model.JITSessionStatusOpen {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestJITSessionStatus_IsTerminal(t *testing.T) {
	if model.JITSessionStatusOpen.IsTerminal() {
		t.Fatal("open must not be terminal")
	}
	for _, s := range []model.JITSessionStatus{model.JITSessionStatusClosed, model.JITSessionStatusFailed} {
		if !s.IsTerminal() {
			t.Fatalf("%q must be terminal", s)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/model/... -run TestJITSession -v`
Expected: compile failure.

- [ ] **Step 3: Implement**

```go
// pkg/model/jit_session.go
package model

import "time"

// JITSessionID is a stable identifier for one just-in-time connect session.
type JITSessionID string

// JITSessionStatus is the lifecycle state of a JITSession.
type JITSessionStatus string

const (
	JITSessionStatusOpen   JITSessionStatus = "open"
	JITSessionStatusClosed JITSessionStatus = "closed"
	JITSessionStatusFailed JITSessionStatus = "failed"
)

// IsTerminal reports whether the session has settled into a final state.
func (s JITSessionStatus) IsTerminal() bool {
	switch s {
	case JITSessionStatusClosed, JITSessionStatusFailed:
		return true
	default:
		return false
	}
}

// JITSession is a temporary, control-plane-owned tunnel to one resource's
// port within one sandbox (spec Decision 3). ExpiresAt is owned entirely by
// the control plane's reconciler (internal/sandbox/jitreconciler.go) --
// the agent that actually dials the resource never runs its own timer or
// decides independently to close a session; it only ever executes an
// explicit close command.
type JITSession struct {
	ID         JITSessionID     `json:"id" yaml:"id"`
	SandboxID  SandboxID        `json:"sandbox_id" yaml:"sandbox_id"`
	ResourceID ResourceID       `json:"resource_id" yaml:"resource_id"`
	Port       int              `json:"port" yaml:"port"`
	Requester  string           `json:"requester,omitempty" yaml:"requester,omitempty"`
	ExpiresAt  time.Time        `json:"expires_at" yaml:"expires_at"`
	Status     JITSessionStatus `json:"status" yaml:"status"`
	Error      string           `json:"error,omitempty" yaml:"error,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/model/... -run TestJITSession -v`
Expected: PASS (both).

- [ ] **Step 5: Run the full model package test suite**

Run: `go test ./pkg/model/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/model/jit_session.go pkg/model/jit_session_test.go
git commit -m "feat(model): add JITSession

Part of #224."
```

---

### Task 2: Store support for `JITSession`

**Files:**
- Modify: `pkg/store/store.go` (interface, or wherever `store.Store`'s method set lives — check with `grep -n "type Store interface" pkg/store/*.go`)
- Modify: `pkg/store/memory.go`
- Modify: `pkg/store/disk.go`
- Test: `pkg/store/memory_test.go`, `pkg/store/disk_test.go` (or wherever this package's existing `Sandbox` CRUD tests live — check first)

**Interfaces:**
- Produces: `store.Store` gains `PutJITSession`, `GetJITSession`, `ListJITSessions`, `DeleteJITSession` — mirroring whatever the existing `Sandbox` methods' exact signatures/error conventions are (`store.ErrNotFound` on a missing get/delete).

- [ ] **Step 1: Inspect the existing `Sandbox` store methods to copy their exact shape**

Run: `grep -n "Sandbox" pkg/store/store.go`

Use whatever that turns up (`PutSandbox(ctx, model.Sandbox) error`, `GetSandbox(ctx, model.SandboxID) (model.Sandbox, error)`, `ListSandboxes(ctx) ([]model.Sandbox, error)`, `DeleteSandbox(ctx, model.SandboxID) error` is the expected shape based on every call site already seen in Plans 1c/2b) as the exact template for the four new `JITSession` methods below — same parameter order, same error-wrapping convention, same `ErrNotFound` behavior on a missing record.

- [ ] **Step 2: Write the failing tests**

```go
// append to pkg/store/memory_test.go (or wherever Sandbox CRUD is tested in this package)

func TestMemoryStore_JITSessionCRUD(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	session := model.JITSession{ID: "jit-1", SandboxID: "sb-1", ResourceID: "res-1", Port: 22, Status: model.JITSessionStatusOpen}

	if err := st.PutJITSession(ctx, session); err != nil {
		t.Fatalf("PutJITSession: %v", err)
	}
	got, err := st.GetJITSession(ctx, "jit-1")
	if err != nil {
		t.Fatalf("GetJITSession: %v", err)
	}
	if got.ResourceID != "res-1" {
		t.Fatalf("got = %+v", got)
	}

	list, err := st.ListJITSessions(ctx)
	if err != nil {
		t.Fatalf("ListJITSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %+v, want 1 entry", list)
	}

	if err := st.DeleteJITSession(ctx, "jit-1"); err != nil {
		t.Fatalf("DeleteJITSession: %v", err)
	}
	if _, err := st.GetJITSession(ctx, "jit-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetJITSession after delete: err = %v, want ErrNotFound", err)
	}
}
```

Adjust the exact import alias/package name (`store.ErrNotFound` vs a bare `ErrNotFound` if this test file is `package store` internal, not `package store_test`) to match whatever the existing `Sandbox` CRUD tests in this same file already do.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./pkg/store/... -run TestMemoryStore_JITSessionCRUD -v`
Expected: compile failure.

- [ ] **Step 4: Implement on `store.Store` and `MemoryStore`**

Add the four method signatures to the `Store` interface (mirroring the `Sandbox` block exactly), and implement them on `MemoryStore` the same way its `Sandbox` methods are implemented (same map-based storage pattern, same mutex usage).

- [ ] **Step 5: Implement on `DiskStore`**

`pkg/store/disk.go`'s `DiskStore` persists a single in-memory snapshot to disk (per AGENTS.md's description of `pkg/store.DiskStore`) — add a `JITSessions map[model.JITSessionID]model.JITSession` field to whatever top-level persisted struct already holds `Sandboxes`, and implement the four methods the same way the existing `Sandbox` methods on `DiskStore` are implemented (delegate to the in-memory map, then trigger whatever save-to-disk call the existing `Sandbox` mutators already trigger).

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./pkg/store/... -run 'TestMemoryStore_JITSessionCRUD|TestDiskStore.*JITSession' -v`
Expected: PASS.

- [ ] **Step 7: Run the full store package test suite**

Run: `go test ./pkg/store/...`
Expected: PASS, no regressions.

- [ ] **Step 8: Commit**

```bash
git add pkg/store/store.go pkg/store/memory.go pkg/store/disk.go pkg/store/*_test.go
git commit -m "feat(store): add JITSession CRUD

Part of #224."
```

---

### Task 3: agent-side session dial capability

**Files:**
- Modify: `proto/boxyagent/v1/agent.proto` (+ regenerate)
- Modify: `pkg/agentsdk/agent.go`, `embedded.go`, `remote.go`, `remoteclient.go`
- Test: matching `_test.go` files for each

**Interfaces:**
- Produces: `agentsdk.ConnectSessionAgent` — `OpenConnectSession(ctx, provider, sessionID, providerResourceID string, port int) error`, `WriteConnectSessionData(sessionID string, data []byte) error`, `CloseConnectSession(sessionID string) error`, plus a way for the caller to receive inbound data: `SetConnectSessionDataHandler(sessionID string, handler func(data []byte, closed bool, err error))`.

This is the one genuinely new piece of wire plumbing in this plan — every prior agent capability (Plans 1b, 2b) was a single request/response `Command`/`CommandResult` pair. A JIT session is a raw duplex byte pipe that has to stay open for the session's lifetime, multiplexed onto the *same* single `AgentTransportService.Connect` stream every other command already uses (ADR-0005: one stream per agent connection, NAT/firewall-friendly) — so this needs new message types on **both** `ServerMessage` and `AgentMessage` (not just `Command`/`CommandResult`, which only flow one at a time per call), tagged with a session ID so many bytes-frames can interleave with ordinary `Command`/`CommandResult` traffic and with each other across concurrently open sessions.

- [ ] **Step 1: Add the proto messages**

In `proto/boxyagent/v1/agent.proto`, add a new top-level message:

```proto
// ConnectSessionData carries one frame of raw byte-tunnel traffic for one
// open JIT session (spec Decision 3), in either direction. Unlike Command/
// CommandResult (one request, one response), many of these can flow for a
// single session_id over the session's lifetime, interleaved with ordinary
// Command/CommandResult traffic on the same stream. `close` means "the
// sender is finished sending in this direction" (client input EOF, or the
// agent's local dial closing) -- it is not itself an error.
message ConnectSessionData {
  string session_id = 1;
  bytes data = 2;
  bool close = 3;
  string error = 4; // non-empty: the session failed; data/close are unused
}
```

Add a new `Command`/`CommandResult` pair to open a session (opening still fits the request/response shape; only the data that follows doesn't):

```proto
// inside message Command's oneof op, after remove_mesh_peer = 15:
    OpenConnectSessionCommand open_connect_session = 16;

// new message:
message OpenConnectSessionCommand {
  string session_id = 1;
  string resource_id = 2;
  int32 port = 3;
}

// inside message CommandResult's oneof outcome, after remove_mesh_peer = 16:
    google.protobuf.Empty open_connect_session = 17;
```

Add `ConnectSessionData` to both wrapper messages' oneofs:

```proto
// ServerMessage.oneof payload, after log_request = 3:
    ConnectSessionData connect_data = 4;

// AgentMessage.oneof payload, after log_batch = 4:
    ConnectSessionData connect_data = 5;
```

- [ ] **Step 2: Regenerate, lint, build, commit**

Run: `task proto:generate && task proto:lint && go build ./pkg/agentproto/...`

```bash
git add proto/boxyagent/v1/agent.proto pkg/agentproto/boxyagent/v1/agent.pb.go pkg/agentproto/boxyagent/v1/agent_grpc.pb.go
git commit -m "feat(agentproto): add ConnectSessionData + OpenConnectSessionCommand

Part of #224."
```

- [ ] **Step 3: Write the failing tests for `EmbeddedAgent`**

```go
// append to pkg/agentsdk/embedded_test.go

func TestEmbeddedAgent_ConnectSession_DialsAndRelaysBothDirections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	echoedCh := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		echoedCh <- buf[:n]
		conn.Write([]byte("pong"))
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	driver := &fakeDriver{providerType: "docker"} // dialing is agent-local, not driver-specific -- any registered provider type routes here
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}

	receivedCh := make(chan []byte, 1)
	agent.SetConnectSessionDataHandler("sess-1", func(data []byte, closed bool, err error) {
		if err != nil {
			t.Errorf("unexpected session error: %v", err)
			return
		}
		if len(data) > 0 {
			receivedCh <- data
		}
	})

	if err := agent.OpenConnectSession(context.Background(), "docker", "sess-1", "127.0.0.1:"+portStr, port); err != nil {
		t.Fatalf("OpenConnectSession: %v", err)
	}
	if err := agent.WriteConnectSessionData("sess-1", []byte("ping")); err != nil {
		t.Fatalf("WriteConnectSessionData: %v", err)
	}

	select {
	case got := <-echoedCh:
		if string(got) != "ping" {
			t.Fatalf("local dial received %q, want ping", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the local dial to receive data")
	}

	select {
	case got := <-receivedCh:
		if string(got) != "pong" {
			t.Fatalf("handler received %q, want pong", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the session data handler to fire")
	}

	if err := agent.CloseConnectSession("sess-1"); err != nil {
		t.Fatalf("CloseConnectSession: %v", err)
	}
}
```

Note `OpenConnectSession`'s third argument here is `"127.0.0.1:"+portStr` standing in for `providerResourceID` — `EmbeddedAgent`'s implementation (Step 4) dials this string directly via `net.Dial("tcp", target)` rather than resolving it through a driver, since a JIT dial target is always `host:port`-shaped (the caller — `internal/sandbox.Manager`, Task 5 — is responsible for resolving a resource ID to its actual reachable address before calling this; `EmbeddedAgent`/`RemoteAgent` never interpret a resource ID as anything other than an already-dialable address for this specific capability). Document this explicitly in the interface's doc comment in Step 4 so it isn't rediscovered as a surprise later.

Add `"net"`, `"strconv"`, `"time"` to `embedded_test.go`'s imports if not already present.

- [ ] **Step 4: Implement `ConnectSessionAgent` on `EmbeddedAgent`**

In `pkg/agentsdk/agent.go`:

```go
// ConnectSessionAgent is an optional agent capability for JIT native-
// protocol access (spec Decision 3). Unlike every other Agent capability in
// this file, the "resource" argument to OpenConnectSession is always
// already a dialable host:port address, never a provider-specific resource
// ID resolved through a driver -- callers (internal/sandbox.Manager)
// resolve the real address before calling this. This capability dials
// directly, with no driver involved at all, because "connect this raw
// TCP stream to this address" needs no provider-specific knowledge.
type ConnectSessionAgent interface {
	OpenConnectSession(ctx context.Context, provider providersdk.Type, sessionID, dialAddress string, port int) error
	WriteConnectSessionData(sessionID string, data []byte) error
	CloseConnectSession(sessionID string) error
	SetConnectSessionDataHandler(sessionID string, handler func(data []byte, closed bool, err error))
}
```

In `pkg/agentsdk/embedded.go`, add fields to `EmbeddedAgent` for tracking open sessions:

```go
	sessionsMu sync.Mutex
	sessions   map[string]net.Conn
	handlers   map[string]func(data []byte, closed bool, err error)
```

(Add `"net"`, `"sync"` to imports; initialize both maps in `NewEmbeddedAgent`.)

```go
func (a *EmbeddedAgent) SetConnectSessionDataHandler(sessionID string, handler func(data []byte, closed bool, err error)) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	a.handlers[sessionID] = handler
}

func (a *EmbeddedAgent) OpenConnectSession(ctx context.Context, _ providersdk.Type, sessionID, dialAddress string, port int) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", dialAddress)
	if err != nil {
		return fmt.Errorf("dial %s for session %q: %w", dialAddress, sessionID, err)
	}
	a.sessionsMu.Lock()
	a.sessions[sessionID] = conn
	handler := a.handlers[sessionID]
	a.sessionsMu.Unlock()

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 && handler != nil {
				handler(append([]byte(nil), buf[:n]...), false, nil)
			}
			if err != nil {
				if handler != nil {
					if err == io.EOF {
						handler(nil, true, nil)
					} else {
						handler(nil, false, err)
					}
				}
				return
			}
		}
	}()
	return nil
}

func (a *EmbeddedAgent) WriteConnectSessionData(sessionID string, data []byte) error {
	a.sessionsMu.Lock()
	conn, ok := a.sessions[sessionID]
	a.sessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("no open session %q", sessionID)
	}
	_, err := conn.Write(data)
	return err
}

func (a *EmbeddedAgent) CloseConnectSession(sessionID string) error {
	a.sessionsMu.Lock()
	conn, ok := a.sessions[sessionID]
	delete(a.sessions, sessionID)
	delete(a.handlers, sessionID)
	a.sessionsMu.Unlock()
	if !ok {
		return nil // already closed/never opened -- idempotent, matching every other Close in this codebase
	}
	return conn.Close()
}
```

Add `"io"` to `embedded.go`'s imports.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/agentsdk/... -run TestEmbeddedAgent_ConnectSession -v`
Expected: PASS.

- [ ] **Step 6: `RemoteAgent` side (server/client-of-the-agent-connection)**

Write the failing tests first:

```go
// append to pkg/agentsdk/remote_test.go

func TestRemoteAgent_OpenConnectSessionRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.OpenConnectSession(context.Background(), "docker", "sess-1", "10.0.0.5:22", 22)
	}()

	cmd := recvCommand(t, stream.sentCh)
	open := cmd.GetOpenConnectSession()
	if open == nil || open.GetSessionId() != "sess-1" || open.GetResourceId() != "10.0.0.5:22" || open.GetPort() != 22 {
		t.Fatalf("unexpected OpenConnectSessionCommand: %#v", open)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_OpenConnectSession{OpenConnectSession: &emptypb.Empty{}},
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("OpenConnectSession: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

func TestRemoteAgent_WriteConnectSessionData_SendsAFrame(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	if err := a.WriteConnectSessionData("sess-1", []byte("ping")); err != nil {
		t.Fatalf("WriteConnectSessionData: %v", err)
	}
	msg := <-stream.sentCh
	data := msg.GetConnectData()
	if data == nil || data.GetSessionId() != "sess-1" || string(data.GetData()) != "ping" {
		t.Fatalf("unexpected ConnectSessionData frame: %#v", data)
	}
}

func TestRemoteAgent_ConnectSessionData_InboundFrameInvokesHandler(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	receivedCh := make(chan []byte, 1)
	closedCh := make(chan struct{}, 1)
	a.SetConnectSessionDataHandler("sess-1", func(data []byte, closed bool, err error) {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		if closed {
			closedCh <- struct{}{}
			return
		}
		receivedCh <- data
	})

	stream.recvCh <- &boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_ConnectData{ConnectData: &boxyagentv1.ConnectSessionData{SessionId: "sess-1", Data: []byte("pong")}},
	}
	select {
	case got := <-receivedCh:
		if string(got) != "pong" {
			t.Fatalf("got %q, want pong", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the handler to fire")
	}

	stream.recvCh <- &boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_ConnectData{ConnectData: &boxyagentv1.ConnectSessionData{SessionId: "sess-1", Close: true}},
	}
	select {
	case <-closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the close notification")
	}
}
```

Run: `go test ./pkg/agentsdk/... -run 'TestRemoteAgent_(OpenConnectSession|WriteConnectSessionData|ConnectSessionData)' -v`
Expected: compile failure (undefined methods/fields).

Implement in `pkg/agentsdk/remote.go`. Add fields to `RemoteAgent`'s struct:

```go
	connectHandlersMu sync.Mutex
	connectHandlers   map[string]func(data []byte, closed bool, err error)
```

Add a case to `Serve`'s receive-loop switch, alongside `case *boxyagentv1.AgentMessage_Result:`:

```go
		case *boxyagentv1.AgentMessage_ConnectData:
			a.deliverConnectData(payload.ConnectData)
```

Add the methods:

```go
func (a *RemoteAgent) deliverConnectData(data *boxyagentv1.ConnectSessionData) {
	a.connectHandlersMu.Lock()
	handler := a.connectHandlers[data.GetSessionId()]
	a.connectHandlersMu.Unlock()
	if handler == nil {
		return // no one is listening for this session any more (already closed) -- not an error
	}
	if data.GetError() != "" {
		handler(nil, false, fmt.Errorf("%s", data.GetError()))
		return
	}
	if len(data.GetData()) > 0 {
		handler(data.GetData(), false, nil)
	}
	if data.GetClose() {
		handler(nil, true, nil)
	}
}

func (a *RemoteAgent) SetConnectSessionDataHandler(sessionID string, handler func(data []byte, closed bool, err error)) {
	a.connectHandlersMu.Lock()
	defer a.connectHandlersMu.Unlock()
	if a.connectHandlers == nil {
		a.connectHandlers = make(map[string]func(data []byte, closed bool, err error))
	}
	a.connectHandlers[sessionID] = handler
}

func (a *RemoteAgent) OpenConnectSession(ctx context.Context, provider providersdk.Type, sessionID, dialAddress string, port int) error {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op: &boxyagentv1.Command_OpenConnectSession{OpenConnectSession: &boxyagentv1.OpenConnectSessionCommand{
			SessionId: sessionID, ResourceId: dialAddress, Port: int32(port),
		}},
	})
	if err != nil {
		return err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return reconstructAgentError(a.info.ID, agentErr)
	}
	return nil
}

// WriteConnectSessionData/CloseConnectSession send a raw frame directly,
// unlike every other RemoteAgent method in this file -- they don't go
// through a.call, because that helper correlates exactly one Command to
// exactly one CommandResult and returns; a session's data frames have no
// per-frame response at all. They reuse a.sendMu/a.stream.Send directly,
// the same underlying primitive a.call itself uses to write the stream.
func (a *RemoteAgent) WriteConnectSessionData(sessionID string, data []byte) error {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	return a.stream.Send(&boxyagentv1.ServerMessage{
		Payload: &boxyagentv1.ServerMessage_ConnectData{ConnectData: &boxyagentv1.ConnectSessionData{SessionId: sessionID, Data: data}},
	})
}

func (a *RemoteAgent) CloseConnectSession(sessionID string) error {
	a.connectHandlersMu.Lock()
	delete(a.connectHandlers, sessionID)
	a.connectHandlersMu.Unlock()
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	return a.stream.Send(&boxyagentv1.ServerMessage{
		Payload: &boxyagentv1.ServerMessage_ConnectData{ConnectData: &boxyagentv1.ConnectSessionData{SessionId: sessionID, Close: true}},
	})
}
```

Add `"sync"` to `remote.go`'s imports if not already present, and `"google.golang.org/protobuf/types/known/emptypb"` to `remote_test.go`'s if not already there (reuse whichever prior task in this plan set already added it rather than duplicating the import).

Run: `go test ./pkg/agentsdk/... -run 'TestRemoteAgent_(OpenConnectSession|WriteConnectSessionData|ConnectSessionData)' -v`
Expected: PASS (all three).

- [ ] **Step 7: `remoteclient.go` — agent-side dial + relay**

`dispatchCommands` (`remoteclient.go`) already has a precedent for exactly this shape: `executeStreamingCommand(ctx, drivers, cmd, s.send)` — a command whose result is a sequence of pushed frames, not one reply — used for `sandbox exec`'s live-streaming mode. This task follows the identical shape.

Write the failing test first:

```go
// append to pkg/agentsdk/remoteclient_test.go

func TestExecuteOpenConnectSession_DialsAndRelaysBothDirections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn) // echo
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	s := &clientSession{}
	sentCh := make(chan *boxyagentv1.AgentMessage, 8)
	sendFn := func(msg *boxyagentv1.AgentMessage) error {
		sentCh <- msg
		return nil
	}

	cmd := &boxyagentv1.Command{
		CommandId: "cmd-1",
		Op: &boxyagentv1.Command_OpenConnectSession{OpenConnectSession: &boxyagentv1.OpenConnectSessionCommand{
			SessionId: "sess-1", ResourceId: "127.0.0.1:" + portStr, Port: int32(port),
		}},
	}
	executeOpenConnectSession(context.Background(), s, cmd, sendFn)

	// First message must be the ack.
	ack := <-sentCh
	if ack.GetResult().GetOpenConnectSession() == nil {
		t.Fatalf("expected an OpenConnectSession ack first, got %#v", ack)
	}

	s.deliverConnectSessionData(&boxyagentv1.ConnectSessionData{SessionId: "sess-1", Data: []byte("ping")})

	select {
	case msg := <-sentCh:
		data := msg.GetConnectData()
		if data == nil || string(data.GetData()) != "ping" {
			t.Fatalf("expected echoed data 'ping', got %#v", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for echoed data")
	}
}
```

Add `"net"`, `"strconv"` to `remoteclient_test.go`'s imports if not already present.

Run: `go test ./pkg/agentsdk/... -run TestExecuteOpenConnectSession -v`
Expected: compile failure.

Implement. Add a field to `clientSession`:

```go
	connectDialsMu sync.Mutex
	connectDials   map[string]net.Conn
```

In `dispatchCommands`, before the existing `cmd := msg.GetCommand()` handling, add a case for inbound session data (arrives as a `ServerMessage`, not wrapped in `Command` — check `ServerMessage`'s accessor name, `msg.GetConnectData()`, generated alongside the `connect_data` oneof field added in Step 1):

```go
		if connectData := msg.GetConnectData(); connectData != nil {
			s.deliverConnectSessionData(connectData)
			continue
		}
```

In the per-command goroutine dispatch, alongside the existing `if update := cmd.GetUpdate(); update != nil && update.GetStream() { ... }` check:

```go
			if open := cmd.GetOpenConnectSession(); open != nil {
				executeOpenConnectSession(ctx, s, cmd, s.send)
				return
			}
```

Add the new functions:

```go
// deliverConnectSessionData routes an inbound data frame to the local dial
// already opened for its session_id (see executeOpenConnectSession).
// Frames for an unknown/already-closed session_id are silently dropped --
// the dial side already tore itself down and told the server so via its
// own close frame; this is not a new error to report.
func (s *clientSession) deliverConnectSessionData(data *boxyagentv1.ConnectSessionData) {
	s.connectDialsMu.Lock()
	conn, ok := s.connectDials[data.GetSessionId()]
	s.connectDialsMu.Unlock()
	if !ok {
		return
	}
	if data.GetClose() {
		conn.Close()
		return
	}
	if len(data.GetData()) > 0 {
		_, _ = conn.Write(data.GetData())
	}
}

// executeOpenConnectSession dials the target directly -- no driver
// involved, since "open a raw TCP connection to this address" needs no
// provider-specific knowledge (matching EmbeddedAgent.OpenConnectSession's
// identical reasoning) -- acks the open, then relays everything the dial
// sends back as outbound ConnectSessionData frames via send, exactly the
// same "push frames instead of returning once" shape executeStreamingCommand
// already established for exec's live-streaming mode.
func executeOpenConnectSession(ctx context.Context, s *clientSession, cmd *boxyagentv1.Command, send func(*boxyagentv1.AgentMessage) error) {
	open := cmd.GetOpenConnectSession()
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", open.GetResourceId())
	if err != nil {
		_ = send(&boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_Result{
			Result: errorResult(cmd.GetCommandId(), fmt.Sprintf("dial %s: %v", open.GetResourceId(), err), err),
		}})
		return
	}
	s.connectDialsMu.Lock()
	if s.connectDials == nil {
		s.connectDials = make(map[string]net.Conn)
	}
	s.connectDials[open.GetSessionId()] = conn
	s.connectDialsMu.Unlock()

	_ = send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Result{Result: &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_OpenConnectSession{OpenConnectSession: &emptypb.Empty{}},
		}},
	})

	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			_ = send(&boxyagentv1.AgentMessage{
				Payload: &boxyagentv1.AgentMessage_ConnectData{ConnectData: &boxyagentv1.ConnectSessionData{
					SessionId: open.GetSessionId(), Data: append([]byte(nil), buf[:n]...),
				}},
			})
		}
		if err != nil {
			closeMsg := &boxyagentv1.ConnectSessionData{SessionId: open.GetSessionId(), Close: true}
			if err != io.EOF {
				closeMsg.Error = err.Error()
			}
			_ = send(&boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_ConnectData{ConnectData: closeMsg}})
			s.connectDialsMu.Lock()
			delete(s.connectDials, open.GetSessionId())
			s.connectDialsMu.Unlock()
			return
		}
	}
}
```

Note the test above calls `send(&boxyagentv1.AgentMessage{...})` for the ack via the ordinary `AgentMessage_Result` payload (not a new oneof variant) -- an `OpenConnectSessionCommand`'s ack is a completely ordinary `CommandResult`, just delivered via the streaming `send` callback instead of the goroutine's own return value, since this dispatch path (matching `executeStreamingCommand`'s precedent) never returns a value the caller reads directly.

Add `"net"` to `remoteclient.go`'s imports if not already present (it doesn't currently import it — every other command in this file operates through `drivers`, never a raw dial).

Run: `go test ./pkg/agentsdk/... -run TestExecuteOpenConnectSession -v`
Expected: PASS.

- [ ] **Step 8: Run the full agentsdk package test suite, then the full repository build/test/lint**

Run: `go test ./pkg/agentsdk/...`
Expected: PASS, no regressions.

Run: `go build ./... && go test ./...`
Expected: PASS everywhere (rerun the known pre-existing Windows `t.TempDir()` `internal/cli` flake in isolation if it appears).

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 9: Commit**

```bash
git add pkg/agentsdk/
git commit -m "feat(agentsdk): ConnectSessionAgent -- open/write/close a raw dial, both agent kinds

Part of #224."
```

---

### Task 4: `POST /api/v1/sandboxes/{id}/connect` — the duplex HTTP relay

**Files:**
- Create: `internal/server/api_connect.go`
- Modify: `internal/server/api_pools.go` (route registration) or wherever routes are registered (check with `grep -n "api/v1/sandboxes/{id}/exec" internal/server/*.go` — register alongside it)
- Modify: `internal/server/api_catalog.go` (route catalog entry, matching the existing exec entries' shape)
- Test: `internal/server/api_connect_test.go`

**Interfaces:**
- Consumes: `agentsdk.ConnectSessionAgent` (Task 3), `store.JITSession` CRUD (Task 2).
- Produces: the HTTP endpoint.

**Design note on the duplex HTTP shape:** no `http.Hijacker`, no protocol upgrade, no WebSocket. A plain `net/http` handler can stream both directions of a single HTTP/1.1 (or HTTP/2) request concurrently: read `r.Body` incrementally in one goroutine (each `Read` call blocks until the client has more to send, exactly like a socket) while writing to `w` and calling `http.NewResponseController(w).Flush()` after each write in another. Go's `net/http` server does not buffer either side waiting for the other to finish — this is the same mechanism `kubectl port-forward`-style tools rely on, just without SPDY's extra multiplexing (not needed here: one HTTP request already maps to exactly one JIT session, no multiplexing required at this layer).

- [ ] **Step 1: Write the failing test**

```go
// internal/server/api_connect_test.go
package server_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestHandleConnect_RelaysBothDirectionsToALocalEchoServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn) // echo
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"},
	}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-1", State: model.ResourceStateAllocated}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	// See this test file's fake agent registration helper (mirror whatever
	// api_exec_test.go already uses to inject a fake executor reachable
	// from a resource ID -- reuse that exact fixture, don't build a new
	// one) configured to dial 127.0.0.1:<portStr> for res-1.
	mux := server.NewTestMuxWithConnectTarget(st, sandbox.New(st, nil), "res-1", "127.0.0.1:"+portStr)

	reader, writer := io.Pipe()
	req := server.AuthedRequest(httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/connect?resource_id=res-1&port=1", reader))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(rec, req)
		close(done)
	}()

	if _, err := writer.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		if bytes.Contains(rec.Body.Bytes(), []byte("ping")) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for echoed data, got so far: %q", rec.Body.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	writer.Close()
	<-done
}

func TestHandleConnect_UnknownResourceIDInSandboxRejected(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	req := server.AuthedRequest(httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/connect?resource_id=not-in-this-sandbox&port=22", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want a client error for a resource ID not in this sandbox", rec.Code)
	}
}
```

`server.NewTestMuxWithConnectTarget` is a new test helper this task adds (mirroring whatever `NewTestMux`/`NewTestMuxWithPoolAdmin` already do) that wires a fake `agentsdk.ConnectSessionAgent`-implementing agent reachable for the given resource, dialing the given address — check `api_exec_test.go` for its equivalent fake-executor wiring helper first and follow its exact construction pattern rather than inventing a new one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/... -run TestHandleConnect -v`
Expected: compile failure — the route/handler/test helper don't exist yet.

- [ ] **Step 3: Implement the handler**

```go
// internal/server/api_connect.go
package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// defaultJITSessionIdleTimeout/MaxLifetime bound how long a JITSession can
// stay open with no data flowing / at all, matching the idle+max-lifetime
// shape sandbox exec's own timeout handling already uses. The control
// plane's reconciler (internal/sandbox/jitreconciler.go) owns enforcing
// these -- this handler only sets the session's initial ExpiresAt.
const (
	defaultJITSessionIdleTimeout = 15 * time.Minute
	maxJITSessionLifetime        = 4 * time.Hour
)

// handleConnect opens a JIT session (spec Decision 3): a full-duplex byte
// tunnel between the caller's HTTP request/response bodies and one
// resource's port, relayed through the target agent, torn down when the
// request ends (client disconnect) or the control-plane reconciler expires
// it.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleUser, model.APIKeyRoleAdmin) {
		return
	}
	sbID := model.SandboxID(r.PathValue("id"))
	sb, err := s.store.GetSandbox(r.Context(), sbID)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "sandbox not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get sandbox")
		return
	}

	resourceID := model.ResourceID(r.URL.Query().Get("resource_id"))
	found := false
	for _, id := range sb.Resources {
		if id == resourceID {
			found = true
			break
		}
	}
	if !found {
		httpjson.Error(w, http.StatusBadRequest, "resource_id is not part of this sandbox")
		return
	}
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port <= 0 || port > 65535 {
		httpjson.Error(w, http.StatusBadRequest, "port must be a valid TCP port number")
		return
	}

	resource, err := s.store.GetResource(r.Context(), resourceID)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "resource not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get resource")
		return
	}

	relay, ok := s.poolMaintenance.(interface {
		ConnectSessionAgentFor(resourceID model.ResourceID) (agentsdk.ConnectSessionAgent, providersdk.Type, string, error)
	})
	if !ok {
		httpjson.Error(w, http.StatusServiceUnavailable, "JIT connect is not available")
		return
	}
	agent, providerType, dialAddress, err := relay.ConnectSessionAgentFor(resourceID)
	if err != nil {
		httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	sessionID := model.JITSessionID(newSessionID())
	session := model.JITSession{
		ID: sessionID, SandboxID: sbID, ResourceID: resourceID, Port: port,
		Requester: principalFromRequest(r).OwnerIdentity(),
		ExpiresAt: time.Now().UTC().Add(defaultJITSessionIdleTimeout),
		Status:    model.JITSessionStatusOpen,
	}
	if err := s.store.PutJITSession(r.Context(), session); err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to record session")
		return
	}

	inbound := make(chan []byte, 16)
	inboundErr := make(chan error, 1)
	agent.SetConnectSessionDataHandler(string(sessionID), func(data []byte, closed bool, err error) {
		if err != nil {
			select {
			case inboundErr <- err:
			default:
			}
			return
		}
		if closed {
			close(inbound)
			return
		}
		inbound <- data
	})
	if err := agent.OpenConnectSession(r.Context(), providerType, string(sessionID), dialAddress, port); err != nil {
		httpjson.Error(w, http.StatusBadGateway, fmt.Sprintf("open connect session: %v", err))
		return
	}
	defer func() {
		_ = agent.CloseConnectSession(string(sessionID))
		session.Status = model.JITSessionStatusClosed
		_ = s.store.PutJITSession(r.Context(), session)
	}()

	flusher := http.NewResponseController(w)
	w.WriteHeader(http.StatusOK)

	// Client -> agent, in this goroutine.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				if writeErr := agent.WriteConnectSessionData(string(sessionID), buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Agent -> client, in this (the handler's own) goroutine.
	for {
		select {
		case data, ok := <-inbound:
			if !ok {
				return
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			_ = flusher.Flush()
		case err := <-inboundErr:
			slog.Default().Error("JIT session failed", "session_id", sessionID, "error", err)
			return
		case <-r.Context().Done():
			return
		}
	}
}
```

Add `"log/slog"` and whatever this file needs for `agentsdk`/`providersdk` imports and `newSessionID()` (reuse this package's existing sandbox-ID-generation helper if one already exists in scope — check `grep -n "func newSandboxID\|func newSessionID" internal/server/*.go internal/sandbox/*.go` first rather than inventing a second ID scheme).

`ConnectSessionAgentFor` above is a new method added to `pool.Manager` (the concrete type `s.poolMaintenance` already is in production, per `internal/cli/serve.go`'s wiring), following the exact same `AgentRegistry.Get` resolution shape as Plan 1c's `SegmentDestroyingProvisioner`:

```go
// internal/pool/manager.go, near DestroyResource/DestroySegment

// ConnectSessionAgentFor resolves the agent that owns res (the exact agent
// that created it, per res.Provider.AgentID -- never re-resolved by
// provider type, same reasoning as every other per-resource lifecycle call
// in this file) and the address to dial it at. Returns an error if the
// resource has no recorded agent, the agent is unavailable, the agent
// doesn't support agentsdk.ConnectSessionAgent, or the resource has no
// dialable address recorded.
func (m *Manager) ConnectSessionAgentFor(resourceID model.ResourceID) (agentsdk.ConnectSessionAgent, providersdk.Type, string, error) {
	agentProvisioner, ok := m.provisioner.(*AgentProvisioner)
	if !ok {
		return nil, "", "", fmt.Errorf("JIT connect requires an agent-backed provisioner")
	}
	res, err := m.store.GetResource(context.Background(), resourceID)
	if err != nil {
		return nil, "", "", fmt.Errorf("get resource %q: %w", resourceID, err)
	}
	agent, ok := agentProvisioner.Registry.Get(res.Provider.AgentID)
	if !ok {
		return nil, "", "", fmt.Errorf("agent %q unavailable for resource %q", res.Provider.AgentID, resourceID)
	}
	sessionAgent, ok := agent.(agentsdk.ConnectSessionAgent)
	if !ok {
		return nil, "", "", fmt.Errorf("agent %q does not support JIT connect", res.Provider.AgentID)
	}
	dialAddress := res.ConnectionInfo["host"]
	if dialAddress == "" {
		return nil, "", "", fmt.Errorf("resource %q has no dialable address recorded", resourceID)
	}
	spec, ok := agentProvisioner.Specs[res.EffectivePool()]
	if !ok {
		return nil, "", "", fmt.Errorf("unknown pool %q for resource %q", res.EffectivePool(), resourceID)
	}
	return sessionAgent, agentProvisioner.driverTypeForPool(spec), dialAddress, nil
}
```

`res.ConnectionInfo["host"]` assumes every driver populates a `"host"` key in `providersdk.Resource.ConnectionInfo` (that field's own doc comment already says its keys are driver-defined, e.g. `"host"`, `"port"`, `"container_id"` — but this is the first place in the codebase reading it back out again, unlike today's exec path, which resolves a resource's address through a completely different route: whatever guest-exec mechanism `execution_manager.go` already uses (PowerShell Direct for Hyper-V, `docker exec` for Docker), neither of which involves dialing a raw TCP address at all. Verify each real driver's `Create` actually sets `ConnectionInfo["host"]` to something dialable (`grep -n "ConnectionInfo\[.host.\]" pkg/providersdk/providers/*/driver.go`) before trusting this key exists project-wide; adjust the key name (or add a fallback chain) if a driver uses something else.

- [ ] **Step 4: Register the route and catalog entry**

Alongside the existing exec route registrations:

```go
mux.HandleFunc("POST /api/v1/sandboxes/{id}/connect", s.handleConnect)
```

Add a matching entry to `api_catalog.go`'s route table, same shape as the exec entries.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/server/... -run TestHandleConnect -v`
Expected: PASS (both).

- [ ] **Step 6: Run the full server package test suite**

Run: `go test ./internal/server/...`
Expected: PASS, no regressions.

- [ ] **Step 7: Commit**

```bash
git add internal/server/api_connect.go internal/server/api_connect_test.go internal/server/api_catalog.go internal/server/api_pools.go
git commit -m "feat(server): add POST /api/v1/sandboxes/{id}/connect

Duplex HTTP relay for JIT native-protocol access -- no Hijack, no
protocol upgrade, plain concurrent read/flush on the existing net/http
server, per this task's design note.

Part of #224."
```

---

### Task 5: control-plane reconciler for `JITSession` expiry

**Files:**
- Create: `internal/sandbox/jitreconciler.go`
- Test: `internal/sandbox/jitreconciler_test.go`
- Modify: `internal/cli/serve.go` (wire the reconciler into the daemon's tick loop, alongside `sandbox.DeletionReconciler`)

**Interfaces:**
- Consumes: `store.JITSession` CRUD (Task 2).
- Produces: `JITSessionReconciler.Reconcile(ctx) error`, closing any session past `ExpiresAt`.

This mirrors `internal/sandbox/deleter.go`'s `DeletionReconciler` almost exactly — same shape, different record type and a different close action (there's no agent-side "close" command to issue here beyond marking the record; the actual live HTTP connection tearing down happens because `handleConnect`'s own goroutines are watching `r.Context().Done()`/the session channel, not because this reconciler reaches into a live TCP dial itself. This reconciler's real job is the *record*: mark expired sessions closed so they stop showing as "open" and so `handleConnect`'s own idle/max-lifetime check — Step 3 below — has something to compare against).

- [ ] **Step 1: Write the failing test**

```go
// internal/sandbox/jitreconciler_test.go
package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestJITSessionReconciler_ClosesExpiredSessions(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	past := time.Now().UTC().Add(-time.Minute)
	if err := st.PutJITSession(ctx, model.JITSession{ID: "jit-1", Status: model.JITSessionStatusOpen, ExpiresAt: past}); err != nil {
		t.Fatalf("PutJITSession: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour)
	if err := st.PutJITSession(ctx, model.JITSession{ID: "jit-2", Status: model.JITSessionStatusOpen, ExpiresAt: future}); err != nil {
		t.Fatalf("PutJITSession: %v", err)
	}

	r := NewJITSessionReconciler(st)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	expired, err := st.GetJITSession(ctx, "jit-1")
	if err != nil {
		t.Fatalf("GetJITSession jit-1: %v", err)
	}
	if expired.Status != model.JITSessionStatusClosed {
		t.Fatalf("jit-1 status = %q, want closed", expired.Status)
	}
	stillOpen, err := st.GetJITSession(ctx, "jit-2")
	if err != nil {
		t.Fatalf("GetJITSession jit-2: %v", err)
	}
	if stillOpen.Status != model.JITSessionStatusOpen {
		t.Fatalf("jit-2 status = %q, want still open", stillOpen.Status)
	}
}

func TestJITSessionReconciler_IgnoresAlreadyTerminalSessions(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	past := time.Now().UTC().Add(-time.Minute)
	if err := st.PutJITSession(ctx, model.JITSession{ID: "jit-1", Status: model.JITSessionStatusFailed, ExpiresAt: past, Error: "dial refused"}); err != nil {
		t.Fatalf("PutJITSession: %v", err)
	}

	r := NewJITSessionReconciler(st)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got, err := st.GetJITSession(ctx, "jit-1")
	if err != nil {
		t.Fatalf("GetJITSession: %v", err)
	}
	if got.Error != "dial refused" {
		t.Fatal("reconciler must not overwrite an already-failed session's recorded error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/... -run TestJITSessionReconciler -v`
Expected: compile failure.

- [ ] **Step 3: Implement**

```go
// internal/sandbox/jitreconciler.go
package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// JITSessionReconciler closes JITSessions past their ExpiresAt. Mirrors
// DeletionReconciler's shape: the control plane owns every session's
// timer, ticked periodically (see internal/cli/serve.go's wiring) --
// the agent that actually holds the dial never runs a timer of its own,
// only ever executes an explicit close command (issued by handleConnect's
// own request-scoped goroutines noticing the session's record went
// terminal, not by this reconciler reaching into a live connection).
type JITSessionReconciler struct {
	store store.Store
	clock Clock
}

func NewJITSessionReconciler(st store.Store) *JITSessionReconciler {
	return &JITSessionReconciler{store: st, clock: realClock{}}
}

// SetClock overrides the reconciler's time source. Used by tests.
func (r *JITSessionReconciler) SetClock(c Clock) {
	if c != nil {
		r.clock = c
	}
}

func (r *JITSessionReconciler) now() time.Time {
	if r.clock == nil {
		return time.Now().UTC()
	}
	return r.clock.Now()
}

func (r *JITSessionReconciler) Reconcile(ctx context.Context) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("JIT session reconciler is not configured")
	}
	sessions, err := r.store.ListJITSessions(ctx)
	if err != nil {
		return fmt.Errorf("list JIT sessions: %w", err)
	}
	now := r.now()
	for _, session := range sessions {
		if session.Status.IsTerminal() {
			continue
		}
		if session.ExpiresAt.After(now) {
			continue
		}
		session.Status = model.JITSessionStatusClosed
		if err := r.store.PutJITSession(ctx, session); err != nil {
			return fmt.Errorf("close expired JIT session %q: %w", session.ID, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run TestJITSessionReconciler -v`
Expected: PASS (both).

- [ ] **Step 5: Wire it into `boxy serve`'s tick loop**

In `internal/cli/serve.go`, find where `sandbox.NewDeletionReconciler` is constructed and ticked (`grep -n "NewDeletionReconciler\|sandboxDeleter" internal/cli/serve.go`); add `jitReconciler := sandbox.NewJITSessionReconciler(st)` alongside it, and call `jitReconciler.Reconcile(ctx)` in the same tick loop iteration `sandboxDeleter.Reconcile(ctx)` already runs in (same 10-second interval, no new ticker needed).

- [ ] **Step 6: Run the full sandbox package test suite, then the full repository build/test/lint**

Run: `go test ./internal/sandbox/...`
Expected: PASS, no regressions.

Run: `go build ./... && go test ./...`
Expected: PASS everywhere (rerun the known pre-existing Windows `t.TempDir()` `internal/cli` flake in isolation if it appears).

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/jitreconciler.go internal/sandbox/jitreconciler_test.go internal/cli/serve.go
git commit -m "feat(sandbox): reconcile expired JITSessions on the existing tick loop

Part of #224."
```

---

### Task 6: `boxy connect` CLI command

**Files:**
- Create: `internal/cli/sandbox_connect.go`
- Test: `internal/cli/sandbox_connect_test.go`
- Modify: `docs/cli-wireframe.md`, `internal/skills/assets/boxy-cli/` (per this project's CLI Change Checklist — any new command requires both)

**Interfaces:**
- Consumes: `POST /api/v1/sandboxes/{id}/connect` (Task 4).
- Produces: `boxy connect <sandbox> --resource <id> --port <n>` — exposes a local TCP listener, one connection at a time, tunneling to the resource.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/sandbox_connect_test.go
package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunSandboxConnect_TunnelsLocalConnectionToServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sandboxes/sb-1/connect", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		w.Write(buf[:n]) // echo whatever the local client sent
		w.(http.Flusher).Flush()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	localAddr, errCh := startConnectTunnel(ctx, srv.URL, "sb-1", "res-1", 22) // startConnectTunnel is this task's testable core, wrapped by the cobra command
	select {
	case err := <-errCh:
		t.Fatalf("startConnectTunnel: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	conn, err := net.Dial("tcp", localAddr)
	if err != nil {
		t.Fatalf("dial local tunnel: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("got %q, want hello", buf[:n])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/... -run TestRunSandboxConnect -v`
Expected: compile failure — `startConnectTunnel` undefined.

- [ ] **Step 3: Implement**

```go
// internal/cli/sandbox_connect.go
package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"
)

func newSandboxConnectCommand() *cobra.Command {
	var opts struct {
		server     string
		resourceID string
		port       int
		localPort  int
	}
	cmd := &cobra.Command{
		Use:   "connect <sandbox>",
		Short: "Open a temporary tunnel to one resource's port for the duration of this command",
		Long: `Opens a local TCP listener that tunnels to the given resource's port
through the daemon -- no standing exposure, no client-side install. Point
your native client (ssh, mstsc, etc.) at the printed local address; the
tunnel closes when this command exits.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base := apiBaseURL(resolveServerOrDefault(opts.server, cmd))
			localAddr, errCh := startConnectTunnel(cmd.Context(), base, args[0], opts.resourceID, opts.port)
			fmt.Fprintf(cmd.OutOrStdout(), "Tunnel open at %s -- Ctrl+C to close\n", localAddr)
			return <-errCh
		},
	}
	cmd.Flags().StringVar(&opts.server, "server", "", "server address (overrides BOXY_SERVER and the global client default)")
	cmd.Flags().StringVar(&opts.resourceID, "resource", "", "resource ID within the sandbox (required)")
	cmd.Flags().IntVar(&opts.port, "port", 0, "the resource's port to tunnel to (required)")
	_ = cmd.MarkFlagRequired("resource")
	_ = cmd.MarkFlagRequired("port")
	return cmd
}

// startConnectTunnel is the testable core of `boxy connect`: it starts a
// local listener immediately (returning its address) and relays every
// accepted connection to the server's /connect endpoint in the background,
// reporting any listener-level (not per-connection) error on errCh.
func startConnectTunnel(ctx context.Context, base, sandboxID, resourceID string, port int) (string, <-chan error) {
	errCh := make(chan error, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		errCh <- err
		return "", errCh
	}
	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				errCh <- nil // listener closed (ctx done) -- not a real error
				return
			}
			go relayConnectSession(ctx, base, sandboxID, resourceID, port, conn)
		}
	}()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	return ln.Addr().String(), errCh
}

func relayConnectSession(ctx context.Context, base, sandboxID, resourceID string, port int, conn net.Conn) {
	defer conn.Close()
	reqURL := fmt.Sprintf("%s/api/v1/sandboxes/%s/connect?resource_id=%s&port=%d",
		base, url.PathEscape(sandboxID), url.QueryEscape(resourceID), port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, conn)
	if err != nil {
		return
	}
	resp, err := apiClientForServer(base).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	io.Copy(conn, resp.Body)
}
```

Check `resolveServerOrDefault` against whatever this file's neighbors (`status.go`'s `resolveServerAddr`) actually name this kind of helper — reuse the existing one rather than inventing a differently-named duplicate.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/... -run TestRunSandboxConnect -v`
Expected: PASS.

- [ ] **Step 5: Register the command**

Find wherever `newStatusCommand`/`newSandboxCreateCommand`-shaped commands are added to the root/`sandbox` command tree (`grep -n "AddCommand(newSandbox" internal/cli/*.go`) and add `cmd.AddCommand(newSandboxConnectCommand())` alongside them.

- [ ] **Step 6: Update the CLI docs (CLI Change Checklist)**

Add a `boxy sandbox connect` entry to `docs/cli-wireframe.md`, matching the existing `sandbox exec` entry's format, and update `internal/skills/assets/boxy-cli/` per this project's CLI Change Checklist. Run `task skills:check` to confirm `internal/skills/drift_test.go`'s command-token coverage still passes.

- [ ] **Step 7: Run the full cli package test suite, `task skills:check`, and the full repository build/test/lint**

Run: `go test ./internal/cli/...`
Expected: PASS, no regressions.

Run: `task skills:check`
Expected: PASS.

Run: `go build ./... && go test ./...`
Expected: PASS everywhere.

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/sandbox_connect.go internal/cli/sandbox_connect_test.go internal/cli/*.go docs/cli-wireframe.md internal/skills/assets/boxy-cli/
git commit -m "feat(cli): add 'boxy sandbox connect' for JIT native-protocol access

Closes #224 (Decision 3 -- JIT access -- complete). Decision 4
(AccessBroker) remains a separate, not-yet-written plan."
```

---

## After This Plan

Decisions 1–3 of the spec are fully implemented. Only Decision 4 (the `AccessBroker` extension point — an interface only, no implementation, per the spec) remains, in its own plan.
