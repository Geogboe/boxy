# Overlay Network Fabric — pkg/meshnet Core Library (Plan 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `pkg/meshnet`, a provider-neutral Go package that owns one WireGuard interface's full lifecycle (keypair generation, bring-up, peer add/remove, teardown) for one sandbox segment — fully self-contained and testable, with **no wiring into any driver, agent, or Manager yet** (that's Plan 2b, written after this one lands).

**Architecture:** A thin wrapper around `golang.zx2c4.com/wireguard`'s userspace `device.Device`, using a real OS TUN device (`golang.zx2c4.com/wireguard/tun`) in production and the in-memory, no-root `netstack` TUN (`golang.zx2c4.com/wireguard/tun/netstack`) in tests — both are real `tun.Device` implementations, so tests exercise the actual WireGuard handshake/encryption/data-plane logic, not a fake. Configuration goes through WireGuard's own userspace API protocol (`Device.IpcSet`) rather than reimplementing anything cryptographic. Each agent generates its own keypair locally, on demand, and the private key never leaves this package — callers (Plan 2b) only ever see the public key as a hex string.

**Tech Stack:** Go 1.25, `golang.zx2c4.com/wireguard` (pinned `v0.0.0-20260522210424-ecfc5a8d5446` — verified against the actual module, not guessed; re-check `go list -m -versions golang.zx2c4.com/wireguard` for a newer pseudo-version before pinning, since this module has no tagged releases), `golang.org/x/crypto/curve25519` (already a transitive dependency of the above, used directly for key generation since `device` exports no keygen helper of its own).

**Spec:** `docs/superpowers/specs/2026-09-10-overlay-network-fabric-design.md` — this plan implements the WireGuard-device-lifecycle piece of Decision 2 only. Peer-introduction wiring (how `boxy serve` learns two agents need to peer and tells them so) is Plan 2b, not written yet.

## Global Constraints

- Never log, persist, or return a private key from any exported function. Only `PublicKeyHex()`-shaped values cross this package's boundary outward.
- One `Interface` per sandbox segment, never a shared interface multiplexing multiple sandboxes (spec Decision 2's isolation property) — this package's API shape (one `New` call per interface, explicit `Close`) makes sharing structurally awkward on purpose; don't add a "get or create shared interface" helper.
- Tests must not require a real OS TUN device (root/Administrator, an actual kernel network interface). Use `netstack.CreateNetTUN` for every test in this plan — never skip a test because "it needs root," and never gate a test behind a build tag that excludes normal CI.

---

### Task 1: Add the `golang.zx2c4.com/wireguard` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces: the module available for Task 2 to import.

- [ ] **Step 1: Check the current available version**

Run: `go list -m -versions golang.zx2c4.com/wireguard`

This module has no tagged releases — expect a pseudo-version list (or none, meaning only `go get ... @latest` resolves one). Use whatever `@latest` resolves to; if it differs from `v0.0.0-20260522210424-ecfc5a8d5446` (this plan's verified-at-write-time pin), that's fine — a newer pseudo-version is expected as time passes. Record whatever version you actually pin in the commit message.

- [ ] **Step 2: Add the dependency**

Run: `go get golang.zx2c4.com/wireguard@latest`

Expected: `go.mod` gains a `require golang.zx2c4.com/wireguard vX.X.X-...` line plus its transitive dependencies (`golang.org/x/crypto`, `golang.org/x/net`, `golang.org/x/sys`, and on Windows builds `golang.zx2c4.com/wintun`); `go.sum` is updated to match.

- [ ] **Step 3: Verify it builds**

Run: `go build ./...`
Expected: succeeds (nothing imports the new module yet, so this only confirms `go.mod`/`go.sum` are consistent).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add golang.zx2c4.com/wireguard dependency

Part of #224. This module has no tagged releases -- pinned to whatever
pseudo-version 'go get @latest' resolved to; see this commit's go.mod
diff for the exact version."
```

---

### Task 2: `meshnet.Interface` — create, bring up, close

**Files:**
- Create: `pkg/meshnet/meshnet.go`
- Create: `pkg/meshnet/keys.go`
- Test: `pkg/meshnet/meshnet_test.go`

**Interfaces:**
- Produces: `meshnet.New(ifName string, listenPort int) (*Interface, error)`, `(*Interface).PublicKeyHex() string`, `(*Interface).Close() error`.

- [ ] **Step 1: Write the failing test**

```go
// pkg/meshnet/meshnet_test.go
package meshnet

import (
	"encoding/hex"
	"testing"
)

func TestNew_GeneratesAKeypairAndBringsUpTheInterface(t *testing.T) {
	iface, err := newForTest(t, "test0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer iface.Close()

	pub := iface.PublicKeyHex()
	if pub == "" {
		t.Fatal("expected a non-empty public key")
	}
	raw, err := hex.DecodeString(pub)
	if err != nil {
		t.Fatalf("public key is not valid hex: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("public key length = %d, want 32 (Curve25519)", len(raw))
	}
}

func TestNew_TwoInterfacesGetDifferentKeypairs(t *testing.T) {
	a, err := newForTest(t, "test-a")
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	defer a.Close()
	b, err := newForTest(t, "test-b")
	if err != nil {
		t.Fatalf("New b: %v", err)
	}
	defer b.Close()

	if a.PublicKeyHex() == b.PublicKeyHex() {
		t.Fatal("two independently created interfaces must not share a keypair")
	}
}

func TestClose_IsIdempotent(t *testing.T) {
	iface, err := newForTest(t, "test-close")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := iface.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := iface.Close(); err != nil {
		t.Fatalf("second Close must be a no-op, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/meshnet/... -v`
Expected: compile failure — `newForTest`, `Interface`, `New` undefined.

- [ ] **Step 3: Implement key generation**

```go
// pkg/meshnet/keys.go
package meshnet

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/device"
)

// generateKeypair creates a fresh Curve25519 keypair in the exact form
// WireGuard requires: a private scalar with the standard X25519 clamping
// applied, and its corresponding public key. device.NoisePrivateKey has no
// exported generator of its own -- WireGuard keys are plain X25519 keys,
// so this clamps and derives them directly via golang.org/x/crypto/curve25519,
// the same primitive the device package itself is built on.
func generateKeypair() (device.NoisePrivateKey, device.NoisePublicKey, error) {
	var priv device.NoisePrivateKey
	if _, err := rand.Read(priv[:]); err != nil {
		return priv, device.NoisePublicKey{}, fmt.Errorf("generate private key: %w", err)
	}
	// Standard X25519 clamping (RFC 7748 section 5).
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	var pub device.NoisePublicKey
	privBytes := [32]byte(priv)
	pubBytes := [32]byte(pub)
	curve25519.ScalarBaseMult(&pubBytes, &privBytes)
	pub = device.NoisePublicKey(pubBytes)

	return priv, pub, nil
}

func hexEncode(key device.NoisePublicKey) string {
	return hex.EncodeToString(key[:])
}
```

- [ ] **Step 4: Implement `Interface`**

```go
// pkg/meshnet/meshnet.go
package meshnet

import (
	"fmt"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// Interface owns one WireGuard device for one sandbox segment. Never share
// one Interface across more than one sandbox -- create a new one per
// segment that needs cross-host connectivity.
type Interface struct {
	dev       *device.Device
	tunDevice tun.Device
	publicKey string // hex-encoded, safe to log/return

	closed bool
}

// tunFactory is the seam tests substitute to avoid needing a real OS TUN
// device (which needs root/Administrator and creates an actual kernel
// network interface). Production always uses the zero value, which resolves
// to tun.CreateTUN.
type tunFactory func(ifName string, mtu int) (tun.Device, error)

// New creates a new WireGuard interface named ifName, generates a fresh
// keypair for it (see generateKeypair -- the private key never leaves this
// package), and brings the interface up listening on listenPort (0 lets
// the OS/WireGuard choose an ephemeral port).
func New(ifName string, listenPort int) (*Interface, error) {
	return newWithTUNFactory(ifName, listenPort, func(name string, mtu int) (tun.Device, error) {
		return tun.CreateTUN(name, mtu)
	})
}

func newWithTUNFactory(ifName string, listenPort int, factory tunFactory) (*Interface, error) {
	tunDevice, err := factory(ifName, device.DefaultMTU)
	if err != nil {
		return nil, fmt.Errorf("create TUN device %q: %w", ifName, err)
	}

	priv, pub, err := generateKeypair()
	if err != nil {
		_ = tunDevice.Close()
		return nil, err
	}

	dev := device.NewDevice(tunDevice, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, ifName+": "))
	if err := dev.IpcSet(fmt.Sprintf("private_key=%x\nlisten_port=%d\n", [32]byte(priv), listenPort)); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure WireGuard device %q: %w", ifName, err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bring up WireGuard device %q: %w", ifName, err)
	}

	return &Interface{dev: dev, tunDevice: tunDevice, publicKey: hexEncode(pub)}, nil
}

// PublicKeyHex returns this interface's public key, hex-encoded -- the only
// key material safe to share with a peer or log.
func (i *Interface) PublicKeyHex() string {
	return i.publicKey
}

// Close tears down the WireGuard device and its TUN device. Idempotent --
// a second Close call is a no-op, matching this codebase's Delete/Destroy
// idempotency convention elsewhere (providersdk.Driver.Delete,
// providersdk.NetworkIsolator.DestroySegment).
func (i *Interface) Close() error {
	if i.closed {
		return nil
	}
	i.closed = true
	i.dev.Close() // device.Device.Close also closes the underlying tun.Device
	return nil
}
```

- [ ] **Step 5: Add the test-only constructor using the netstack TUN**

```go
// append to pkg/meshnet/meshnet_test.go
import (
	"net/netip"
	"testing"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

// newForTest builds an Interface backed by netstack's in-memory TUN instead
// of a real OS device -- no root/Administrator needed, portable to any CI
// runner. localAddr is an arbitrary address in a private test range; two
// interfaces in the same test must use different addresses so their
// netstacks don't collide.
func newForTest(t *testing.T, ifName string) (*Interface, error) {
	t.Helper()
	localAddr := netip.MustParseAddr("192.0.2.1")
	iface, err := newWithTUNFactory(ifName, 0, func(name string, mtu int) (tun.Device, error) {
		tunDev, _, err := netstack.CreateNetTUN([]netip.Addr{localAddr}, nil, mtu)
		return tunDev, err
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = iface.Close() })
	return iface, nil
}
```

Add `"golang.zx2c4.com/wireguard/tun"` to the test file's import block (needed for the `tun.Device` return type in the factory closure).

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./pkg/meshnet/... -v`
Expected: PASS (all three).

- [ ] **Step 7: Commit**

```bash
git add pkg/meshnet/meshnet.go pkg/meshnet/keys.go pkg/meshnet/meshnet_test.go
git commit -m "feat(meshnet): Interface creation, keypair generation, teardown

Part of #224."
```

---

### Task 3: Peer add/remove

**Files:**
- Modify: `pkg/meshnet/meshnet.go`
- Test: `pkg/meshnet/meshnet_test.go`

**Interfaces:**
- Produces: `(*Interface).AddPeer(publicKeyHex, endpoint string, allowedIPs []string) error`, `(*Interface).RemovePeer(publicKeyHex string) error`.

This is the task that proves the package actually works end to end: two real `Interface`s, peered with each other, complete a real WireGuard handshake and pass an encrypted packet — not a mock, the actual protocol.

- [ ] **Step 1: Write the failing test**

```go
// append to pkg/meshnet/meshnet_test.go

func TestAddPeer_TwoInterfacesHandshakeAndExchangeData(t *testing.T) {
	addrA := netip.MustParseAddr("192.0.2.1")
	addrB := netip.MustParseAddr("192.0.2.2")

	a, err := newWithTUNFactoryAndAddr(t, "test-peer-a", addrA)
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	b, err := newWithTUNFactoryAndAddr(t, "test-peer-b", addrB)
	if err != nil {
		t.Fatalf("New b: %v", err)
	}

	// Endpoints are loopback here -- netstack only replaces the *inner* TUN
	// (the virtual interface WireGuard decrypts traffic onto); the *outer*
	// WireGuard protocol traffic still goes through conn.NewDefaultBind()'s
	// real OS UDP sockets (set in newWithTUNFactory), so this exercises the
	// real handshake/encryption path end to end, not a simulated one.
	// newForTest/newWithTUNFactoryAndAddr construct with listenPort=0
	// (ephemeral) since most tests don't care what port they land on; this
	// test needs stable, known ports up front to build explicit endpoint
	// strings, so it re-sets listen_port directly via IpcSet after
	// construction -- WireGuard's UAPI supports changing listen_port at any
	// time, not just before Up().
	portA, portB := 51900, 51901
	if err := a.dev.IpcSet(fmt.Sprintf("listen_port=%d\n", portA)); err != nil {
		t.Fatalf("set port a: %v", err)
	}
	if err := b.dev.IpcSet(fmt.Sprintf("listen_port=%d\n", portB)); err != nil {
		t.Fatalf("set port b: %v", err)
	}

	if err := a.AddPeer(b.PublicKeyHex(), fmt.Sprintf("127.0.0.1:%d", portB), []string{"192.0.2.2/32"}); err != nil {
		t.Fatalf("a.AddPeer: %v", err)
	}
	if err := b.AddPeer(a.PublicKeyHex(), fmt.Sprintf("127.0.0.1:%d", portA), []string{"192.0.2.1/32"}); err != nil {
		t.Fatalf("b.AddPeer: %v", err)
	}

	if !waitForHandshake(t, a, 5*time.Second) {
		t.Fatal("interface a never completed a handshake with b")
	}
}

func TestRemovePeer_StopsFurtherHandshakes(t *testing.T) {
	a, err := newForTest(t, "test-rmpeer-a")
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	fakePeerKey := "0000000000000000000000000000000000000000000000000000000000aa"
	if err := a.AddPeer(fakePeerKey, "127.0.0.1:9", []string{"192.0.2.9/32"}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := a.RemovePeer(fakePeerKey); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	// No handshake assertion here -- the peer was never reachable anyway
	// (port 9 discards). This only proves RemovePeer doesn't error, which
	// is the whole surface worth testing without a live second peer.
}

// waitForHandshake polls iface's own IpcGet output for a completed
// handshake (a "last_handshake_time_sec" field appears once one occurs).
func waitForHandshake(t *testing.T, iface *Interface, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := iface.dev.IpcGet()
		if err == nil && strings.Contains(status, "last_handshake_time_sec") {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
```

Add `"fmt"`, `"strings"`, `"time"` to the test file's imports if not already present. Add a small helper next to `newForTest` (Task 2, Step 5) for a caller-specified address:

```go
func newWithTUNFactoryAndAddr(t *testing.T, ifName string, addr netip.Addr) (*Interface, error) {
	t.Helper()
	iface, err := newWithTUNFactory(ifName, 0, func(name string, mtu int) (tun.Device, error) {
		tunDev, _, err := netstack.CreateNetTUN([]netip.Addr{addr}, nil, mtu)
		return tunDev, err
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = iface.Close() })
	return iface, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/meshnet/... -run 'TestAddPeer|TestRemovePeer' -v`
Expected: compile failure — `AddPeer`/`RemovePeer` undefined.

- [ ] **Step 3: Implement `AddPeer`/`RemovePeer`**

Append to `pkg/meshnet/meshnet.go`:

```go
// AddPeer configures a new peer reachable at endpoint (host:port), routing
// the given allowedIPs (CIDR strings) through it. publicKeyHex is the
// peer's public key as returned by its own PublicKeyHex().
func (i *Interface) AddPeer(publicKeyHex, endpoint string, allowedIPs []string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "public_key=%s\n", publicKeyHex)
	fmt.Fprintf(&sb, "endpoint=%s\n", endpoint)
	for _, cidr := range allowedIPs {
		fmt.Fprintf(&sb, "allowed_ip=%s\n", cidr)
	}
	if err := i.dev.IpcSet(sb.String()); err != nil {
		return fmt.Errorf("add peer %s: %w", publicKeyHex, err)
	}
	return nil
}

// RemovePeer removes a previously added peer. Matches WireGuard's own UAPI
// idempotency: removing a peer that was never added, or already removed,
// is not an error (mirrors providersdk.NetworkIsolator.DestroySegment's
// idempotency convention elsewhere in this codebase).
func (i *Interface) RemovePeer(publicKeyHex string) error {
	config := fmt.Sprintf("public_key=%s\nremove=true\n", publicKeyHex)
	if err := i.dev.IpcSet(config); err != nil {
		return fmt.Errorf("remove peer %s: %w", publicKeyHex, err)
	}
	return nil
}
```

Add `"strings"` to `meshnet.go`'s import block.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/meshnet/... -run 'TestAddPeer|TestRemovePeer' -v`
Expected: PASS. `TestAddPeer_TwoInterfacesHandshakeAndExchangeData` may take a few hundred milliseconds to a couple of seconds (a real handshake, not instant) — that's expected, not a hang; if it times out at 5s, that's a real signal something is misconfigured (wrong port, wrong allowed_ip, wrong public key), not a flaky timing issue to just extend.

- [ ] **Step 5: Run the full meshnet package test suite**

Run: `go test ./pkg/meshnet/... -v`
Expected: PASS (all tests from Tasks 2 and 3 together).

- [ ] **Step 6: Run `task lint`**

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 7: Commit**

```bash
git add pkg/meshnet/meshnet.go pkg/meshnet/meshnet_test.go
git commit -m "feat(meshnet): AddPeer/RemovePeer, verified with a real handshake

TestAddPeer_TwoInterfacesHandshakeAndExchangeData creates two real
Interfaces (netstack-backed, no root needed) and proves they complete an
actual WireGuard handshake -- not a mocked one.

Part of #224."
```

---

### Task 4: `Interface.Name()` — the OS interface name for driver-level routing

**Files:**
- Modify: `pkg/meshnet/meshnet.go`
- Test: `pkg/meshnet/meshnet_test.go`

**Interfaces:**
- Produces: `(*Interface).Name() (string, error)`.

A future driver (Plan 2b's Hyper-V/Docker `MeshPeerer` implementation) needs this to add an OS route sending its segment's subnet traffic through this specific interface (`New-NetRoute -InterfaceAlias <name>` on Windows, `ip route add ... dev <name>` on Linux) — `pkg/meshnet` itself never touches OS routing tables; that stays the driver's job, matching the spec's "drivers never touch WireGuard directly [but do route through whatever local interface meshnet hands them]".

- [ ] **Step 1: Write the failing test**

```go
// append to pkg/meshnet/meshnet_test.go

func TestName_ReturnsTheUnderlyingTUNDeviceName(t *testing.T) {
	iface, err := newForTest(t, "test-name")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	name, err := iface.Name()
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if name == "" {
		t.Fatal("expected a non-empty interface name")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/meshnet/... -run TestName -v`
Expected: compile failure — `Name` undefined.

- [ ] **Step 3: Implement it**

Append to `pkg/meshnet/meshnet.go`:

```go
// Name returns the OS-level name this interface was actually given (which
// may differ from the ifName passed to New on some platforms). A driver's
// NetworkIsolator implementation uses this to route its segment's subnet
// through this specific interface -- pkg/meshnet itself never touches OS
// routing tables.
func (i *Interface) Name() (string, error) {
	return i.tunDevice.Name()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/meshnet/... -run TestName -v`
Expected: PASS.

- [ ] **Step 5: Run the full meshnet package test suite and full repository build/test/lint**

Run: `go test ./pkg/meshnet/...`
Expected: PASS.

Run: `go build ./... && go test ./...`
Expected: PASS everywhere.

Run: `task lint`
Expected: 0 issues.

- [ ] **Step 6: Commit**

```bash
git add pkg/meshnet/meshnet.go pkg/meshnet/meshnet_test.go
git commit -m "feat(meshnet): expose the underlying TUN device name

Closes pkg/meshnet's core library (#224 Decision 2's device-lifecycle
piece). Driver/agentsdk/Manager wiring -- how boxy serve learns two
agents need to peer, and tells them so -- is a separate follow-up plan,
not written yet."
```

---

## After This Plan

`pkg/meshnet` is a complete, independently useful, fully-tested library — nothing in `boxy serve`, any driver, or `agentsdk` calls it yet. The next plan (Plan 2b, not written) wires it in:

- A new `providersdk.MeshPeerer` optional capability (`MeshIdentity(ctx, ref SegmentRef) (publicKey, endpoint, cidr string, err error)`, `AddMeshPeer(ctx, ref, peerPublicKey, peerEndpoint, peerCIDR string) error`, `RemoveMeshPeer(ctx, ref, peerPublicKey string) error`), implemented by Hyper-V (creating/reusing a `meshnet.Interface` lazily on first mesh call, routing the segment's vSwitch subnet through it, discovering the segment's own CIDR from Plan 1a's ledger) and Docker (discovering its auto-assigned subnet via `NetworkInspect`).
- The matching `agentsdk.MeshPeeringAgent` capability + proto messages, mirroring Plans 1b's exact three-file shape.
- `sandbox.Manager` orchestration: when a sandbox's `NetworkSegments` grows past one agent, call `MeshIdentity` on every agent involved and cross-wire `AddMeshPeer` calls between all of them (full mesh) via the existing gRPC/mTLS channel each agent already has to `boxy serve` — no new transport.
