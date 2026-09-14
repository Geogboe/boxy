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

func TestAgentProvisioner_IsANetworkIsolatingAllocator(t *testing.T) {
	var a sandbox.SandboxAllocator = &pool.AgentProvisioner{}
	if _, ok := a.(sandbox.NetworkIsolatingAllocator); !ok {
		t.Fatal("*AgentProvisioner must satisfy sandbox.NetworkIsolatingAllocator")
	}
}
