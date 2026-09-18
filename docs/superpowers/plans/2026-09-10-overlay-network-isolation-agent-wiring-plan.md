# Overlay Network Fabric — Agent Wiring (Plan 1b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `providersdk.NetworkIsolator` (Plan 1a) reachable from `boxy serve` through both agent kinds — in-process (`EmbeddedAgent`) and remote gRPC (`RemoteAgent`) — so the control plane can call `CreateSegment`/`AttachToSegment`/`DestroySegment` on whichever agent actually hosts the resource, regardless of whether that agent runs in the same process or on a different machine.

**Architecture:** A new optional `agentsdk.NetworkIsolatingAgent` capability, following exactly the same three-file pattern `GuestPersonalizingAgent` already established: `EmbeddedAgent` dispatches straight to the local driver via type assertion; `RemoteAgent` sends new proto `Command` variants over the existing gRPC stream and decodes the `CommandResult`; the agent-side dispatch loop in `remoteclient.go`'s `executeCommand` receives those commands and calls the same local driver type assertion `EmbeddedAgent` uses. No new transport, no new stream — this rides the identical bidirectional stream ADR-0005 already established.

**Tech Stack:** Go 1.25, protobuf/gRPC (pinned `buf` CLI via `task proto:generate`).

**Spec:** `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` — this plan is the remote-agent-forwarding half of Decision 1 (the spec's Decision 2, cross-host WireGuard mesh, is intentionally untouched here — that's a separate future plan). It does **not** wire anything into `internal/sandbox`/`internal/pool` allocation or deletion flows — that's Plan 1c, written after this one lands.

## Global Constraints

- Mirror `GuestPersonalizingAgent`'s exact shape at every layer (interface location, method signature style, proto message naming, dispatch switch structure, the "unsupported driver → empty result, not an error" convention). Do not invent a different shape for `NetworkIsolatingAgent` than the one already established for the near-identical `GuestPersonalizingAgent` capability.
- `AttachToSegment` must stay fast end-to-end — no added retry/backoff/polling at the agentsdk layer either.
- These are provider-neutral wire types (`providersdk.SegmentRef` is a plain string) — do not introduce Hyper-V- or Docker-specific fields into the proto messages.

---

### Task 1: Proto messages — `CreateSegmentCommand`, `AttachToSegmentCommand`, `DestroySegmentCommand`

**Files:**
- Modify: `proto/boxyagent/v1/agent.proto`
- Regenerate: `pkg/agentproto/boxyagent/v1/agent.pb.go`, `pkg/agentproto/boxyagent/v1/agent_grpc.pb.go` (generated, not hand-edited)

**Interfaces:**
- Produces: `boxyagentv1.Command_CreateSegment`, `boxyagentv1.Command_AttachToSegment`, `boxyagentv1.Command_DestroySegment` (oneof variants), `boxyagentv1.CreateSegmentCommand`, `boxyagentv1.AttachToSegmentCommand`, `boxyagentv1.DestroySegmentCommand`, `boxyagentv1.CommandResult_CreateSegment`, `boxyagentv1.CommandResult_AttachToSegment`, `boxyagentv1.CommandResult_DestroySegment`, `boxyagentv1.CreateSegmentResult`.

This task has no Go-level red/green cycle of its own — proto messages aren't unit-testable in isolation — so its "test" is the regeneration succeeding cleanly and the new generated symbols compiling. Task 2 is what actually exercises these types.

- [ ] **Step 1: Add the new `Command` oneof variants**

In `proto/boxyagent/v1/agent.proto`, inside `message Command`'s `oneof op` (after `PersonalizeGuestCommand personalize_guest = 9;`):

```proto
    CreateSegmentCommand create_segment = 10;
    AttachToSegmentCommand attach_to_segment = 11;
    DestroySegmentCommand destroy_segment = 12;
```

- [ ] **Step 2: Add the new command message definitions**

After `message PersonalizeGuestCommand { ... }`:

```proto
// CreateSegmentCommand asks the agent to create a new, empty private
// network segment for one sandbox. See providersdk.NetworkIsolator.
message CreateSegmentCommand {
  string sandbox_id = 1;
}

// AttachToSegmentCommand asks the agent to move an already-created
// resource onto an existing segment. segment_ref is the opaque value a
// prior CreateSegmentResult returned.
message AttachToSegmentCommand {
  string resource_id = 1;
  string segment_ref = 2;
}

// DestroySegmentCommand asks the agent to tear down a segment created by
// CreateSegmentCommand. Must be idempotent for an already-gone segment,
// matching DeleteCommand's contract.
message DestroySegmentCommand {
  string segment_ref = 1;
}
```

- [ ] **Step 3: Add the new `CommandResult` oneof variants**

Inside `message CommandResult`'s `oneof outcome` (after `PersonalizeGuestResult personalize_guest = 9;` and `OperationStreamEvent operation_stream = 10;`):

```proto
    CreateSegmentResult create_segment = 11;
    google.protobuf.Empty attach_to_segment = 12;
    google.protobuf.Empty destroy_segment = 13;
```

- [ ] **Step 4: Add the new result message definition**

After `message PersonalizeGuestResult { ... }`:

```proto
message CreateSegmentResult {
  string segment_ref = 1;
}
```

(`AttachToSegmentCommand`/`DestroySegmentCommand` need no typed result payload — an empty `google.protobuf.Empty` outcome, exactly like `DeleteCommand`'s existing `google.protobuf.Empty deleted = 5`, means "succeeded, nothing to report".)

- [ ] **Step 5: Regenerate the Go stubs**

Run: `task proto:generate`
Expected: `pkg/agentproto/boxyagent/v1/agent.pb.go` and `agent_grpc.pb.go` are rewritten (git diff shows the new message/oneof types added, nothing else changed).

- [ ] **Step 6: Lint the proto and build the regenerated Go package**

Run: `task proto:lint`
Expected: no lint errors.

Run: `go build ./pkg/agentproto/...`
Expected: succeeds.

- [ ] **Step 7: Commit**

```bash
git add proto/boxyagent/v1/agent.proto pkg/agentproto/boxyagent/v1/agent.pb.go pkg/agentproto/boxyagent/v1/agent_grpc.pb.go
git commit -m "feat(agentproto): add CreateSegment/AttachToSegment/DestroySegment wire messages

Part of #224."
```

---

### Task 2: `EmbeddedAgent` implementation

**Files:**
- Modify: `pkg/agentsdk/embedded.go`
- Test: `pkg/agentsdk/embedded_test.go`

**Interfaces:**
- Consumes: `providersdk.NetworkIsolator` (Plan 1a), `EmbeddedAgent.driver(provider)` (existing helper, `embedded.go`).
- Produces: `(*EmbeddedAgent)` satisfies a new `agentsdk.NetworkIsolatingAgent` interface (defined in this task, `agent.go`).

- [ ] **Step 1: Write the failing tests**

```go
// append to pkg/agentsdk/embedded_test.go

// fakeIsolatingDriver adds providersdk.NetworkIsolator on top of a minimal
// driver, mirroring fakePersonalizingDriver's "capability on top of a base
// driver" shape used for the analogous GuestPersonalizer tests in this file.
type fakeIsolatingDriver struct {
	*fakeDriver
	createSegmentRef providersdk.SegmentRef
	createSegmentErr error
	attachErr        error
	destroyErr       error
	gotSandboxID     string
	gotResourceID    string
	gotAttachRef     providersdk.SegmentRef
	gotDestroyRef    providersdk.SegmentRef
}

func (f *fakeIsolatingDriver) CreateSegment(_ context.Context, sandboxID string) (providersdk.SegmentRef, error) {
	f.gotSandboxID = sandboxID
	if f.createSegmentErr != nil {
		return "", f.createSegmentErr
	}
	return f.createSegmentRef, nil
}
func (f *fakeIsolatingDriver) AttachToSegment(_ context.Context, providerResourceID string, ref providersdk.SegmentRef) error {
	f.gotResourceID, f.gotAttachRef = providerResourceID, ref
	return f.attachErr
}
func (f *fakeIsolatingDriver) DestroySegment(_ context.Context, ref providersdk.SegmentRef) error {
	f.gotDestroyRef = ref
	return f.destroyErr
}

func TestEmbeddedAgent_CreateSegment(t *testing.T) {
	driver := &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}, createSegmentRef: "boxy-sb-sb-1"}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	ref, err := agent.CreateSegment(context.Background(), "hyperv", "sb-1")
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if ref != "boxy-sb-sb-1" {
		t.Fatalf("ref = %q, want %q", ref, "boxy-sb-sb-1")
	}
	if driver.gotSandboxID != "sb-1" {
		t.Fatalf("driver got sandboxID = %q, want %q", driver.gotSandboxID, "sb-1")
	}
}

func TestEmbeddedAgent_AttachToSegment(t *testing.T) {
	driver := &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	if err := agent.AttachToSegment(context.Background(), "hyperv", "vm-1", "boxy-sb-sb-1"); err != nil {
		t.Fatalf("AttachToSegment: %v", err)
	}
	if driver.gotResourceID != "vm-1" || driver.gotAttachRef != "boxy-sb-sb-1" {
		t.Fatalf("driver got (%q, %q)", driver.gotResourceID, driver.gotAttachRef)
	}
}

func TestEmbeddedAgent_DestroySegment(t *testing.T) {
	driver := &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	if err := agent.DestroySegment(context.Background(), "hyperv", "boxy-sb-sb-1"); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
	if driver.gotDestroyRef != "boxy-sb-sb-1" {
		t.Fatalf("driver got destroy ref = %q, want %q", driver.gotDestroyRef, "boxy-sb-sb-1")
	}
}

func TestEmbeddedAgent_CreateSegmentUnsupportedDriverErrors(t *testing.T) {
	driver := &fakeDriver{providerType: "docker"}
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", driver)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	if _, err := agent.CreateSegment(context.Background(), "docker", "sb-1"); err == nil {
		t.Fatal("expected an error for a driver that does not implement NetworkIsolator")
	}
}
```

(`fakeDriver` is `embedded_test.go`'s existing base fake, already used by `fakePersonalizingDriver` in that file — reuse it, don't redefine it.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/agentsdk/... -run TestEmbeddedAgent_.*Segment -v`
Expected: compile failure — `agent.CreateSegment` etc. undefined on `*EmbeddedAgent`.

- [ ] **Step 3: Define the `agentsdk.NetworkIsolatingAgent` capability**

In `pkg/agentsdk/agent.go`, after the existing `GuestPersonalizingAgent` interface:

```go
// NetworkIsolatingAgent is an optional agent capability for providers that
// implement providersdk.NetworkIsolator. Unlike GuestPersonalizingAgent
// (which degrades to nil, nil for an unsupported driver so callers fall
// back to the generic Allocate path), there is no fallback here: a caller
// that reaches CreateSegment/AttachToSegment/DestroySegment already
// type-asserted for this capability specifically, so an unsupported
// driver is a caller bug, not an expected degrade path — it should error.
type NetworkIsolatingAgent interface {
	CreateSegment(ctx context.Context, provider providersdk.Type, sandboxID string) (providersdk.SegmentRef, error)
	AttachToSegment(ctx context.Context, provider providersdk.Type, providerResourceID string, ref providersdk.SegmentRef) error
	DestroySegment(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef) error
}
```

- [ ] **Step 4: Implement it on `EmbeddedAgent`**

In `pkg/agentsdk/embedded.go`, add to the `var (...)` compile-time assertion block at the top:

```go
	_ NetworkIsolatingAgent = (*EmbeddedAgent)(nil)
```

Then, after the existing `PersonalizeGuest` method:

```go
func (a *EmbeddedAgent) CreateSegment(ctx context.Context, provider providersdk.Type, sandboxID string) (providersdk.SegmentRef, error) {
	d, err := a.driver(provider)
	if err != nil {
		return "", err
	}
	isolator, ok := d.(providersdk.NetworkIsolator)
	if !ok {
		return "", fmt.Errorf("agent %q: provider %q does not support network isolation", a.info.ID, provider)
	}
	return isolator.CreateSegment(ctx, sandboxID)
}

func (a *EmbeddedAgent) AttachToSegment(ctx context.Context, provider providersdk.Type, providerResourceID string, ref providersdk.SegmentRef) error {
	d, err := a.driver(provider)
	if err != nil {
		return err
	}
	isolator, ok := d.(providersdk.NetworkIsolator)
	if !ok {
		return fmt.Errorf("agent %q: provider %q does not support network isolation", a.info.ID, provider)
	}
	return isolator.AttachToSegment(ctx, providerResourceID, ref)
}

func (a *EmbeddedAgent) DestroySegment(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef) error {
	d, err := a.driver(provider)
	if err != nil {
		return err
	}
	isolator, ok := d.(providersdk.NetworkIsolator)
	if !ok {
		return fmt.Errorf("agent %q: provider %q does not support network isolation", a.info.ID, provider)
	}
	return isolator.DestroySegment(ctx, ref)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/agentsdk/... -run TestEmbeddedAgent_.*Segment -v`
Expected: PASS (all four).

- [ ] **Step 6: Run the full agentsdk package test suite to check for regressions**

Run: `go test ./pkg/agentsdk/...`
Expected: PASS, no regressions.

- [ ] **Step 7: Commit**

```bash
git add pkg/agentsdk/agent.go pkg/agentsdk/embedded.go pkg/agentsdk/embedded_test.go
git commit -m "feat(agentsdk): implement NetworkIsolatingAgent on EmbeddedAgent

Part of #224."
```

---

### Task 3: `RemoteAgent` client-side implementation

**Files:**
- Modify: `pkg/agentsdk/remote.go`
- Test: `pkg/agentsdk/remote_test.go`

**Interfaces:**
- Consumes: `boxyagentv1.Command_CreateSegment`/`AttachToSegment`/`DestroySegment`, `boxyagentv1.CreateSegmentResult` (Task 1), `(a *RemoteAgent) call(ctx, cmd) (*boxyagentv1.CommandResult, error)` (existing, `remote.go:359`), `agentsdk.NetworkIsolatingAgent` (Task 2).
- Produces: `(*RemoteAgent)` satisfies `agentsdk.NetworkIsolatingAgent`.

- [ ] **Step 1: Write the failing tests**

```go
// append to pkg/agentsdk/remote_test.go

func TestRemoteAgent_CreateSegmentRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		ref providersdk.SegmentRef
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		ref, err := a.CreateSegment(context.Background(), "hyperv", "sb-1")
		resultCh <- result{ref, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	createSegment := cmd.GetCreateSegment()
	if createSegment == nil {
		t.Fatalf("expected a CreateSegmentCommand, got %#v", cmd)
	}
	if createSegment.GetSandboxId() != "sb-1" {
		t.Fatalf("expected sandbox_id sb-1, got %q", createSegment.GetSandboxId())
	}

	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_CreateSegment{CreateSegment: &boxyagentv1.CreateSegmentResult{SegmentRef: "boxy-sb-sb-1"}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("CreateSegment returned error: %v", r.err)
		}
		if r.ref != "boxy-sb-sb-1" {
			t.Fatalf("ref = %q, want %q", r.ref, "boxy-sb-sb-1")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CreateSegment to return")
	}
}

func TestRemoteAgent_AttachToSegmentRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.AttachToSegment(context.Background(), "hyperv", "vm-1", "boxy-sb-sb-1")
	}()

	cmd := recvCommand(t, stream.sentCh)
	attach := cmd.GetAttachToSegment()
	if attach == nil {
		t.Fatalf("expected an AttachToSegmentCommand, got %#v", cmd)
	}
	if attach.GetResourceId() != "vm-1" || attach.GetSegmentRef() != "boxy-sb-sb-1" {
		t.Fatalf("expected (vm-1, boxy-sb-sb-1), got (%q, %q)", attach.GetResourceId(), attach.GetSegmentRef())
	}

	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_AttachToSegment{AttachToSegment: &emptypb.Empty{}},
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("AttachToSegment returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for AttachToSegment to return")
	}
}

func TestRemoteAgent_DestroySegmentRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.DestroySegment(context.Background(), "hyperv", "boxy-sb-sb-1")
	}()

	cmd := recvCommand(t, stream.sentCh)
	destroy := cmd.GetDestroySegment()
	if destroy == nil {
		t.Fatalf("expected a DestroySegmentCommand, got %#v", cmd)
	}
	if destroy.GetSegmentRef() != "boxy-sb-sb-1" {
		t.Fatalf("expected segment_ref boxy-sb-sb-1, got %q", destroy.GetSegmentRef())
	}

	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_DestroySegment{DestroySegment: &emptypb.Empty{}},
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("DestroySegment returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for DestroySegment to return")
	}
}

func TestRemoteAgent_CreateSegmentAgentErrorSurfaces(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	resultCh := make(chan error, 1)
	go func() {
		_, err := a.CreateSegment(context.Background(), "hyperv", "sb-1")
		resultCh <- err
	}()

	cmd := recvCommand(t, stream.sentCh)
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_Error{Error: &boxyagentv1.AgentError{Message: "no capacity for another switch"}},
	})

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("expected an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CreateSegment to return")
	}
}
```

Add `"google.golang.org/protobuf/types/known/emptypb"` to `remote_test.go`'s import block if not already present (check first — `remote_test.go` may already import it for the `Delete` round-trip test; reuse the existing import if so, don't duplicate).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/agentsdk/... -run TestRemoteAgent_.*Segment -v`
Expected: compile failure — `a.CreateSegment` etc. undefined on `*RemoteAgent`.

- [ ] **Step 3: Implement the methods**

In `pkg/agentsdk/remote.go`, add to the `var _ GuestPersonalizingAgent = (*RemoteAgent)(nil)` line's block:

```go
var _ NetworkIsolatingAgent = (*RemoteAgent)(nil)
```

Then, after the existing `PersonalizeGuest` method:

```go
func (a *RemoteAgent) CreateSegment(ctx context.Context, provider providersdk.Type, sandboxID string) (providersdk.SegmentRef, error) {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_CreateSegment{CreateSegment: &boxyagentv1.CreateSegmentCommand{SandboxId: sandboxID}},
	})
	if err != nil {
		return "", err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return "", reconstructAgentError(a.info.ID, agentErr)
	}
	return providersdk.SegmentRef(res.GetCreateSegment().GetSegmentRef()), nil
}

func (a *RemoteAgent) AttachToSegment(ctx context.Context, provider providersdk.Type, providerResourceID string, ref providersdk.SegmentRef) error {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op: &boxyagentv1.Command_AttachToSegment{AttachToSegment: &boxyagentv1.AttachToSegmentCommand{
			ResourceId: providerResourceID,
			SegmentRef: string(ref),
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

func (a *RemoteAgent) DestroySegment(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef) error {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_DestroySegment{DestroySegment: &boxyagentv1.DestroySegmentCommand{SegmentRef: string(ref)}},
	})
	if err != nil {
		return err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return reconstructAgentError(a.info.ID, agentErr)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/agentsdk/... -run TestRemoteAgent_.*Segment -v`
Expected: PASS (all four).

- [ ] **Step 5: Run the full agentsdk package test suite to check for regressions**

Run: `go test ./pkg/agentsdk/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/agentsdk/remote.go pkg/agentsdk/remote_test.go
git commit -m "feat(agentsdk): implement NetworkIsolatingAgent on RemoteAgent

Part of #224."
```

---

### Task 4: agent-side dispatch in `remoteclient.go`

**Files:**
- Modify: `pkg/agentsdk/remoteclient.go`
- Test: `pkg/agentsdk/remoteclient_test.go`

**Interfaces:**
- Consumes: `providersdk.NetworkIsolator`, `boxyagentv1.Command_CreateSegment`/`AttachToSegment`/`DestroySegment` (Task 1), the existing `executeCommand(ctx, drivers DriverSet, cmd *boxyagentv1.Command) *boxyagentv1.CommandResult` switch (`remoteclient.go`) and `errorResult(commandID, msg, err)` helper (existing).
- Produces: `executeCommand` handles the three new command types — this is what a real `boxy agent serve` process (any host, Hyper-V or Docker) actually executes when it receives one of Task 3's commands over the wire.

- [ ] **Step 1: Write the failing tests**

```go
// append to pkg/agentsdk/remoteclient_test.go (as new t.Run cases inside
// the existing TestExecuteCommand function, right after the "personalize
// guest driver error is surfaced as AgentError" case)

	t.Run("create segment success", func(t *testing.T) {
		drivers := DriverSet{"hyperv": &fakeIsolatingDriver{
			fakeDriver:       &fakeDriver{providerType: "hyperv"},
			createSegmentRef: "boxy-sb-sb-1",
		}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-20",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_CreateSegment{CreateSegment: &boxyagentv1.CreateSegmentCommand{SandboxId: "sb-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		if got := res.GetCreateSegment().GetSegmentRef(); got != "boxy-sb-sb-1" {
			t.Fatalf("segment_ref = %q, want %q", got, "boxy-sb-sb-1")
		}
		fid := drivers["hyperv"].(*fakeIsolatingDriver)
		if fid.gotSandboxID != "sb-1" {
			t.Fatalf("driver got sandboxID = %q, want %q", fid.gotSandboxID, "sb-1")
		}
	})

	t.Run("create segment driver error is surfaced as AgentError", func(t *testing.T) {
		drivers := DriverSet{"hyperv": &fakeIsolatingDriver{
			fakeDriver:       &fakeDriver{providerType: "hyperv"},
			createSegmentErr: errors.New("no free CIDR blocks"),
		}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-21",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_CreateSegment{CreateSegment: &boxyagentv1.CreateSegmentCommand{SandboxId: "sb-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() == nil {
			t.Fatal("expected an AgentError")
		}
	})

	t.Run("create segment unsupported by driver errors", func(t *testing.T) {
		drivers := DriverSet{"docker": &fakeDriver{providerType: "docker"}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-22",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_CreateSegment{CreateSegment: &boxyagentv1.CreateSegmentCommand{SandboxId: "sb-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() == nil {
			t.Fatal("expected an error for a driver that does not implement NetworkIsolator")
		}
	})

	t.Run("attach to segment success", func(t *testing.T) {
		drivers := DriverSet{"hyperv": &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-23",
			ProviderType: "hyperv",
			Op: &boxyagentv1.Command_AttachToSegment{AttachToSegment: &boxyagentv1.AttachToSegmentCommand{
				ResourceId: "vm-1",
				SegmentRef: "boxy-sb-sb-1",
			}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		if res.GetAttachToSegment() == nil {
			t.Fatalf("expected an AttachToSegment (empty) outcome, got %#v", res.GetOutcome())
		}
		fid := drivers["hyperv"].(*fakeIsolatingDriver)
		if fid.gotResourceID != "vm-1" || fid.gotAttachRef != "boxy-sb-sb-1" {
			t.Fatalf("driver got (%q, %q)", fid.gotResourceID, fid.gotAttachRef)
		}
	})

	t.Run("destroy segment success", func(t *testing.T) {
		drivers := DriverSet{"hyperv": &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-24",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_DestroySegment{DestroySegment: &boxyagentv1.DestroySegmentCommand{SegmentRef: "boxy-sb-sb-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		if res.GetDestroySegment() == nil {
			t.Fatalf("expected a DestroySegment (empty) outcome, got %#v", res.GetOutcome())
		}
		fid := drivers["hyperv"].(*fakeIsolatingDriver)
		if fid.gotDestroyRef != "boxy-sb-sb-1" {
			t.Fatalf("driver got destroy ref = %q, want %q", fid.gotDestroyRef, "boxy-sb-sb-1")
		}
	})
```

(`fakeIsolatingDriver` was defined in `embedded_test.go` by Task 2, same package `agentsdk` — reused here directly, not redefined. `errors` must already be imported in `remoteclient_test.go`; check its import block first and reuse if present.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/agentsdk/... -run TestExecuteCommand -v`
Expected: FAIL on the five new subtests — `executeCommand`'s switch has no case for `Command_CreateSegment`/`Command_AttachToSegment`/`Command_DestroySegment` yet, so they fall into the existing `default: unknown command op` branch, producing an error for all five (including the two that expect success).

- [ ] **Step 3: Add the dispatch cases**

In `pkg/agentsdk/remoteclient.go`'s `executeCommand` switch, add three cases after the existing `case *boxyagentv1.Command_PersonalizeGuest:` block (before `default:`):

```go
	case *boxyagentv1.Command_CreateSegment:
		isolator, ok := d.(providersdk.NetworkIsolator)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q does not support network isolation", cmd.GetProviderType()), nil)
		}
		ref, err := isolator.CreateSegment(ctx, op.CreateSegment.GetSandboxId())
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_CreateSegment{CreateSegment: &boxyagentv1.CreateSegmentResult{SegmentRef: string(ref)}},
		}

	case *boxyagentv1.Command_AttachToSegment:
		isolator, ok := d.(providersdk.NetworkIsolator)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q does not support network isolation", cmd.GetProviderType()), nil)
		}
		if err := isolator.AttachToSegment(ctx, op.AttachToSegment.GetResourceId(), providersdk.SegmentRef(op.AttachToSegment.GetSegmentRef())); err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_AttachToSegment{AttachToSegment: &emptypb.Empty{}},
		}

	case *boxyagentv1.Command_DestroySegment:
		isolator, ok := d.(providersdk.NetworkIsolator)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q does not support network isolation", cmd.GetProviderType()), nil)
		}
		if err := isolator.DestroySegment(ctx, providersdk.SegmentRef(op.DestroySegment.GetSegmentRef())); err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_DestroySegment{DestroySegment: &emptypb.Empty{}},
		}
```

Check `remoteclient.go`'s existing import block for `"google.golang.org/protobuf/types/known/emptypb"` — if `Command_Delete`'s handling already imports it (likely, since `Deleted` is also an `Empty`), reuse that import; otherwise add it.

`d` here is whatever local variable name the surrounding switch already uses for the resolved `providersdk.Driver` (the same one `case *boxyagentv1.Command_PersonalizeGuest:` already dereferences as `gp, ok := d.(providersdk.GuestPersonalizer)`) — match that exact variable name, don't introduce a second one.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/agentsdk/... -run TestExecuteCommand -v`
Expected: PASS (all subtests, including the five new ones).

- [ ] **Step 5: Run the full agentsdk package test suite to check for regressions**

Run: `go test ./pkg/agentsdk/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Run the full repository test suite and lint to check for cross-package regressions**

Run: `go build ./... && go test ./...`
Expected: PASS everywhere (a known pre-existing Windows `t.TempDir()` cleanup flake on `internal/cli` agent-serve tests may appear — rerun that specific test in isolation to confirm it's the documented flake, not a real regression, before treating it as a problem; see AGENTS.md's "Lessons Learned" for this exact flake).

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 7: Commit**

```bash
git add pkg/agentsdk/remoteclient.go pkg/agentsdk/remoteclient_test.go
git commit -m "feat(agentsdk): dispatch CreateSegment/AttachToSegment/DestroySegment to the local driver

Closes the agent-wiring half of #224's driver-native isolation (Decision
1). Nothing calls this from the control plane yet -- fulfiller/allocation
wiring is a separate follow-up plan."
```

---

## After This Plan

Both agent kinds can now execute `NetworkIsolator` operations end-to-end (in-process or over gRPC to a remote host), fully tested with fakes at every layer. **Nothing in `boxy serve` calls any of this yet.** The next plan (not yet written) wires this into `internal/sandbox.Manager`'s allocation loop (where a sandbox's resources actually get claimed — `manager.go:190-245`/`:337`, keyed by a new optional `pool.NetworkIsolatingProvisioner`-style capability on `AgentProvisioner`/`DriverProvisioner`), plus sandbox-deletion teardown and an orphan sweep for segments left behind by a crash — mirroring the existing resource-destroy transient-state pattern (ADR-0006).
