# Provider Architecture

This package defines the `Provider` interface, which is a core abstraction in Boxy. It allows the main application to manage resources without needing to know the specifics of the underlying virtualization or containerization technology. This is an implementation of the **Strategy Pattern**.

The `Provider` interface defines a set of capabilities for managing the lifecycle of a resource, such as `Provision`, `Destroy`, and `GetStatus`.

## Concrete Provider Implementations

Each subdirectory in this package contains a concrete implementation of the `Provider` interface.

-   **`docker/`**: Manages local Docker containers by communicating with the Docker daemon.
-   **`hyperv/`**: Manages local Hyper-V VMs by executing PowerShell commands.
-   **`mock/`**: A "fake" provider used during testing to simulate provider actions without any real-world side effects.
-   **`remote/`**: A special provider that acts as a network client to a remote Boxy Agent, delegating all actions.

## The `remote` and `proto` Relationship

The `remote` and `proto` packages work together to enable Boxy's distributed agent architecture.

-   **`proto/`**: This directory contains the "language" for network communication, defined using **gRPC** and **Protocol Buffers**. The `.proto` files here are the blueprints for the API between a client and a server.
-   **`remote/`**: This provider uses the "language" defined in `proto/` to talk to a Boxy Agent running on another machine. It doesn't manage resources itself; it acts as a **remote control**.

### Workflow Diagram

This diagram shows how a call from the application is handled by the `RemoteProvider`.

```text
[Boxy App]
     |
     v
[Provider Interface]
     |
     v
[RemoteProvider]  (implements the interface)
     |
     +---- (uses generated client code) ---> [gRPC Client Stub]
                                                     |
                                                 (network)
                                                     |
                                                     v
                                          [Remote Boxy Agent Server]
                                                     ^
                                                     |
                               (implements generated server interface)
                                                     ^
                                                     |
                                     (Generated from .proto file)
                                                     ^
                                                     |
                                              [proto/service.proto]

```
