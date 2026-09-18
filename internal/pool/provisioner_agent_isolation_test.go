// This file is deliberately package pool_test (external test package), not
// package pool like provisioner_agent_test.go. internal/sandbox imports
// internal/pool (see fulfiller.go), so an internal ("package pool") test
// file cannot import internal/sandbox without creating an import cycle --
// go test reports it as "import cycle not allowed in test". An external
// test package has no such restriction: pool_test -> sandbox -> pool is not
// a cycle back to pool_test itself. The type assertion below only needs
// AgentProvisioner's exported surface, so it doesn't need any of
// provisioner_agent_test.go's unexported fakes.
package pool_test

import (
	"testing"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
)

// Manager must satisfy sandbox.SegmentDestroyer, which internal/sandbox's
// DeletionReconciler type-asserts for rather than requiring. That assertion
// failing is invisible at runtime -- it just silently stops tearing segments
// down -- and the two signatures are unusually easy to drift apart, because
// sandbox.SegmentDestroyer is declared with plain strings while everything
// on the pool side of the boundary speaks providersdk.Type/SegmentRef (see
// Manager.DestroySegment's own doc comment on why the conversion happens
// there). Pinning the pairing here turns any future drift into a build
// failure instead.
var _ sandbox.SegmentDestroyer = (*pool.Manager)(nil)

func TestAgentProvisioner_IsANetworkIsolatingAllocator(t *testing.T) {
	var a sandbox.SandboxAllocator = &pool.AgentProvisioner{}
	if _, ok := a.(sandbox.NetworkIsolatingAllocator); !ok {
		t.Fatal("*AgentProvisioner must satisfy sandbox.NetworkIsolatingAllocator")
	}
}
