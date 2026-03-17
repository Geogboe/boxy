// Package agentsdk defines the contract between the Boxy server and agents.
//
// An agent is the communications layer for one or more provider drivers.
// The server talks to agents — never to drivers directly. Whether the
// agent is embedded (in-process) or remote (gRPC) is transparent to the
// server; both implement the same Agent interface.
//
// Lifecycle:
//
//  1. Agent starts and registers with the server (token-based auth)
//  2. Agent advertises which provider types it supports
//  3. Server routes CRUD requests to agents based on provider type
//  4. Agent dispatches to the appropriate local driver
package agentsdk

import (
	"context"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

// Agent is the interface the server uses to communicate with any agent,
// whether embedded or remote. It wraps one or more provider drivers and
// routes CRUD operations to them.
type Agent interface {
	// Info returns the agent's identity and the providers it supports.
	Info() AgentInfo

	// Create provisions a resource through the named provider.
	Create(ctx context.Context, provider providersdk.Type, cfg any) (*providersdk.Resource, error)

	// Read returns the current status of a resource.
	Read(ctx context.Context, provider providersdk.Type, id string) (*providersdk.ResourceStatus, error)

	// Update performs an operation on an existing resource.
	Update(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation) (*providersdk.Result, error)

	// Delete destroys a resource.
	Delete(ctx context.Context, provider providersdk.Type, id string) error
}

// AgentInfo describes an agent and the providers it hosts.
type AgentInfo struct {
	// ID is a unique identifier for this agent instance.
	ID string

	// Name is a human-readable label (e.g. "docker-host-1", "lab-hypervisor").
	Name string

	// Providers lists the provider types this agent can handle.
	Providers []providersdk.Type
}
