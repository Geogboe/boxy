// Package agent implements the remote agent server used by Boxy to expose
// local provider resources (Hyper-V, Docker, KVM, mock, etc.) over gRPC.
// It runs on remote machines and allows the central Boxy service to delegate
// provisioning and lifecycle operations to those hosts.
//
// Overview
//
// An agent instance hosts a gRPC server (optionally with mTLS) and exposes any
// registered provider implementations. Incoming RPCs are routed to those
// providers, which handle resource provisioning, destruction, status, exec,
// and connection-info queries. Providers must satisfy the provider.Provider
// interface.
//
// Typical deployment:
//
//   Central Boxy Service
//        |
//        | gRPC (mTLS optional)
//        |
//     Agent Server
//        |
//        +-- Docker Provider
//        +-- Hyper-V Provider
//        +-- Mock Provider
//
// The server manages provider lifecycle, health checks, and conversion between
// internal provider types and protobuf messages.
//
// Usage
//
// Server instances are created with NewServer and configured via Config. After
// creation, providers are registered with RegisterProvider and the server is
// started with Start. The agent performs no automatic provider discovery; the
// caller is responsible for supplying provider implementations appropriate for
// the host.
//
// Security
//
// The agent supports TLS and mutual TLS. When mTLS is enabled, both client and
// server must present certificates signed by a shared CA. Certificate paths and
// TLS settings are supplied through Config.
//
// RPC Surface
//
// The agent implements the ProviderService gRPC interface, supporting
// operations such as:
//
//   - Provision / Destroy
//   - GetStatus
//   - GetConnectionInfo
//   - Exec
//   - Update
//   - HealthCheck
//
// Errors returned by providers are mapped to gRPC status codes.
//
// Architecture Notes
//
//   - convert.go handles all mapping between provider types and protobuf
//     messages, keeping the wire format separate from internal structures.
//   - provider registration is explicit; each provider is injected by the
//     caller and must implement HealthCheck.
//   - logging is structured and includes provider name, agent ID, and resource
//     identifiers.
//
// Non-goals
//
//   - No CLI parsing or flag handling (done in the CLI layer).
//   - No opinionated certificate generation.
//   - No direct domain logic outside provider delegation.
//
package agent
