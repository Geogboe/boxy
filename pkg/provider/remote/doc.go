// Package remote provides a Provider that delegates operations to a remote
// Boxy Agent via gRPC.
//
// Overview
//
// The RemoteProvider makes networked calls to a remote Boxy Agent and
// implements the same Provider interface as local providers. It is primarily
// intended for distributed setups where a central controller issues resource
// requests to remote agents that run and manage local resources.
//
// API Notes
//
// The RPC contract is defined in the `pkg/provider/proto` package; this
// package depends on the generated client code to call the remote agent. The
// RemoteProvider performs translation between Boxy-native payloads and the
// protobuf wire format, including sensible error handling and retries where
// appropriate.
//
// Security
//
// The RemoteProvider must be used with secure transport (mutual TLS) in
// production to protect credentials and control messages.
package remote
