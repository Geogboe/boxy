// Package docker implements a Provider backed by the local Docker daemon.
//
// # Overview
//
// The Docker provider manages container lifecycle via the Docker Engine SDK
// for Go. It maps Boxy ResourceSpec parameters to container create/inspect/run
// workflow steps and supports Exec, Provision, Destroy and status operations.
//
// # Requirements
//
// A local Docker daemon must be available and Boxy must have permissions to
// connect to it. On most systems this means Docker Engine is installed and the
// running user has access to the Docker socket.
//
// # API Notes
//
// The provider relies on the official Docker SDK to perform container actions
// and return typed responses. It does not attempt to implement orchestration
// features beyond creating and managing resources on the local host.
//
// # Security
//
// Containers are first-class resources with connection info returned to the
// caller. The provider does not store container credentials aside from
// intended connection metadata; secrets handling is the caller's responsibility.
//
// # Dependencies
//
// This package depends on the Docker SDK and the Boxy provider interfaces.
package docker
