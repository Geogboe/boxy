// Package client provides a Go SDK for the boxy HTTP API.
//
// This package allows programmatic interaction with a boxy server, providing
// type-safe methods for sandbox and pool operations.
//
// # Usage
//
//	client := client.New("http://localhost:8080")
//
//	// Create a sandbox
//	sandbox, err := client.CreateSandbox(ctx, &client.CreateSandboxRequest{
//		Name: "my-sandbox",
//		Resources: []client.ResourceRequest{
//			{PoolName: "docker-pool", Count: 1},
//		},
//		Duration: 30 * time.Minute,
//	})
//
//	// Wait for sandbox to be ready
//	sandbox, err = client.WaitForSandbox(ctx, sandbox.ID, 5*time.Minute)
//
//	// Get connection information
//	resources, err := client.GetSandboxResources(ctx, sandbox.ID)
//
//	// Destroy sandbox when done
//	err = client.DestroySandbox(ctx, sandbox.ID)
//
// # Status
//
// This package is currently a stub. Implementation pending.
// See TODO in client.go for implementation checklist.
package client
