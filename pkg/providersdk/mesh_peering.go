package providersdk

import "context"

// MeshPeerer is an optional provider capability for cross-host connectivity
// between two sandboxes' segments on different hosts (spec Decision 2). Not
// every driver implements it -- callers must type-assert, the same pattern
// as every other optional capability in this package.
//
// MeshIdentity is the first call for a given segment: it lazily creates the
// segment's mesh interface if one doesn't exist yet, and returns what a
// remote peer needs to connect to it -- a public key (never a private key;
// see pkg/meshnet's key-handling guarantee), a host:port endpoint, and the
// segment's own subnet CIDR (so the *other* side knows what to route
// through this peer once it, in turn, calls AddMeshPeer).
//
// AddMeshPeer/RemoveMeshPeer operate on a segment that MUST have already
// had MeshIdentity called for it at least once -- calling either on a
// segment with no mesh interface yet is a caller error, not something to
// silently no-op.
type MeshPeerer interface {
	MeshIdentity(ctx context.Context, ref SegmentRef) (publicKey, endpoint, cidr string, err error)
	AddMeshPeer(ctx context.Context, ref SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error
	RemoveMeshPeer(ctx context.Context, ref SegmentRef, peerPublicKey string) error
}
