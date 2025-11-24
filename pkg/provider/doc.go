// Package provider defines the provider abstraction used by Boxy to manage
// resources such as VMs and containers.
//
// Overview
//
// The Provider interface is a core domain primitive that decouples Boxy's
// lifecycle orchestration from any particular virtualization or container
// platform. Implementations of this interface translate Boxy actions (eg:
// Provision, Destroy, GetStatus) to platform-specific operations.
//
// Concrete implementations live in subpackages (docker, hyperv, scratch and
// mock) and a RemoteProvider exists to delegate actions to a remote Boxy
// agent over gRPC.
//
// Concrete Provider Implementations
//
// - `docker/`: Manages local Docker containers by communicating with the
//   Docker daemon.
// - `hyperv/`: Manages local Hyper-V VMs using PowerShell/PowerShell Direct.
// - `mock/`: A simple provider used in tests to simulate provider behavior.
// - `remote/`: A network client implementation that delegates calls to a
//   remote Boxy Agent via gRPC.
//
// Remote/Proto Relationship
//
// The `remote` provider and the `proto` packages work together to enable
// distributed operation. The `.proto` files are the single source of truth for
// RPC schemas; the `remote` provider uses generated client code to forward
// requests to a remote Boxy Agent.
//
// Workflow Diagram
//
// ```text
// [Boxy App]
//      |
//      v
// [Provider Interface]
//      |
//      v
// [RemoteProvider]  (implements the interface)
//      |
//      +---- (uses generated client code) ---> [gRPC Client Stub]
//                                                      |
//                                                  (network)
//                                                      |
//                                                      v
//                                           [Remote Boxy Agent Server]
//                                                      ^
//                                                      |
//                                (implements generated server interface)
//                                                      ^
//                                                      |
//                                      (Generated from .proto file)
//                                                      ^
//                                                      |
//                                               [proto/service.proto]
//
// ```
//
// API Notes
//
// Providers are intentionally designed to be small and dumb: they should avoid
// retaining application-level state and act as a thin translation layer.
// Typical provider methods accept context.Context and return simple, serializable
// structures used by higher-level managers.
//
// Security
//
// Providers sometimes generate or handle credentials. Boxy prefers encrypted
// storage of secrets and avoids logging sensitive values. Implementations should
// avoid embedding credentials in logs and should encrypt any secret material
// before persistence.
//
// Dependencies
//
// The provider package itself is an interface package; concrete packages will
// have additional third-party requirements (Docker SDK, Hyper-V PowerShell,
// gRPC/protobuf, etc.).
package provider
