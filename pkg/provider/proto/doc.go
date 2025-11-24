// Package proto contains the protobuf/gRPC service definitions that describe
// the API between a RemoteProvider client and a remote Boxy Agent.
//
// # Overview
//
// The `.proto` files in this package are the single source of truth for the
// distributed API surface. `protoc` is used to generate the Go server and
// client stubs that the `remote` package and the Boxy Agent use to communicate.
//
// # API Notes
//
// These definitions are intentionally minimal and versioned; changes to the
// service contract should be done carefully and follow semver-compatible
// evolution patterns. Generated code is stored in `provider.pb.go` and
// `provider_grpc.pb.go`.
//
// # Dependencies
//
// The package is used by other packages at build time (to generate clients and
// servers) rather than being a runtime dependency that introduces heavy
// coupling in package imports.
package proto
