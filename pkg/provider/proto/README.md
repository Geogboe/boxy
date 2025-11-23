# Protocol Definitions

This package contains the Protocol Buffers (`.proto`) files that define the gRPC API for communication between the `RemoteProvider` (client) and the Boxy Agent (server).

These files are the single source of truth for the distributed API contract. They are used by the `protoc` compiler to generate both the client code (used by the `RemoteProvider`) and the server interface (implemented by the Boxy Agent).
