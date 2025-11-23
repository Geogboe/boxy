# Remote Provider

This package implements the `Provider` interface by acting as a network client to a remote Boxy Agent. Instead of managing local resources, it forwards requests over the network using gRPC.

The API contract for this communication is defined in the `pkg/provider/proto` package. For more details on how this fits into the overall architecture, see the main `README.md` in the parent `pkg/provider/` directory.
