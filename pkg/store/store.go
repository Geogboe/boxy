package store

import (
	"context"
	"errors"

	"github.com/Geogboe/boxy/pkg/model"
)

var ErrNotFound = errors.New("not found")

// Store is the minimal persistence surface the runtime managers need.
//
// This is intentionally small and may be split into narrower interfaces later.
type Store interface {
	// Pools
	GetPool(ctx context.Context, name model.PoolName) (model.Pool, error)
	PutPool(ctx context.Context, pool model.Pool) error

	// Resources
	GetResource(ctx context.Context, id model.ResourceID) (model.Resource, error)
	PutResource(ctx context.Context, res model.Resource) error
	DeleteResource(ctx context.Context, id model.ResourceID) error

	// Sandboxes
	GetSandbox(ctx context.Context, id model.SandboxID) (model.Sandbox, error)
	CreateSandbox(ctx context.Context, sb model.Sandbox) error
	PutSandbox(ctx context.Context, sb model.Sandbox) error
	DeleteSandbox(ctx context.Context, id model.SandboxID) error

	// Agent registration tokens (single-use bootstrap credentials, see
	// docs/adr/0005-remote-agent-transport-and-registration.md). PutAgentToken
	// doubles as both create and mark-used: callers read, mutate UsedAt, and
	// write back, the same upsert convention used for sandboxes/resources.
	GetAgentToken(ctx context.Context, id model.AgentTokenID) (model.AgentRegistrationToken, error)
	PutAgentToken(ctx context.Context, tok model.AgentRegistrationToken) error
	DeleteAgentToken(ctx context.Context, id model.AgentTokenID) error
	ListAgentTokens(ctx context.Context) ([]model.AgentRegistrationToken, error)

	// Revoked agent identities (deny-list, keyed by client certificate
	// serial). No bbolt or other new persistence engine — this reuses the
	// same Store this interface already defines, since revocation counts
	// are expected to stay in the 10s-100s.
	PutRevokedAgentIdentity(ctx context.Context, rev model.RevokedAgentIdentity) error
	IsAgentIdentityRevoked(ctx context.Context, certSerial string) (bool, error)
	ListRevokedAgentIdentities(ctx context.Context) ([]model.RevokedAgentIdentity, error)

	// Agent identities record which cert serial an agent ID currently
	// holds, so `boxy agent revoke <id>` can populate a serial-keyed
	// RevokedAgentIdentity even for a currently-disconnected agent.
	PutAgentIdentity(ctx context.Context, identity model.AgentIdentity) error
	GetAgentIdentity(ctx context.Context, agentID string) (model.AgentIdentity, error)

	// List operations return all entities of a given type.
	// An empty store returns a non-nil, zero-length slice.
	ListPools(ctx context.Context) ([]model.Pool, error)
	ListResources(ctx context.Context) ([]model.Resource, error)
	ListSandboxes(ctx context.Context) ([]model.Sandbox, error)
}
