package agent

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Geogboe/boxy/pkg/provider/mock"
	pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

func TestNewServer(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
			errMsg:  "config cannot be nil",
		},
		{
			name: "missing listen address",
			cfg: &Config{
				AgentID: "test-agent",
			},
			wantErr: true,
			errMsg:  "listen address is required",
		},
		{
			name: "missing agent ID",
			cfg: &Config{
				ListenAddr: ":50051",
			},
			wantErr: true,
			errMsg:  "agent ID is required",
		},
		{
			name: "valid config",
			cfg: &Config{
				AgentID:    "test-agent",
				ListenAddr: ":0",
				UseTLS:     false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := logrus.New()
			logger.SetLevel(logrus.ErrorLevel)

			srv, err := NewServer(tt.cfg, logger)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				assert.Nil(t, srv)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, srv)
				assert.Equal(t, tt.cfg.AgentID, srv.agentID)
			}
		})
	}
}

func TestServer_RegisterProvider(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	cfg := &Config{
		AgentID:    "test-agent",
		ListenAddr: ":0",
		UseTLS:     false,
	}

	srv, err := NewServer(cfg, logger)
	require.NoError(t, err)

	mockProvider := mock.NewProvider(logger, &mock.Config{
		ProvisionDelay: 10 * time.Millisecond,
	})

	// Register provider
	err = srv.RegisterProvider("mock", mockProvider)
	assert.NoError(t, err)

	// Verify provider is registered
	assert.Contains(t, srv.providers, "mock")

	// Try to register same provider again
	err = srv.RegisterProvider("mock", mockProvider)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestServer_GetProvider(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	cfg := &Config{
		AgentID:    "test-agent",
		ListenAddr: ":0",
		UseTLS:     false,
	}

	srv, err := NewServer(cfg, logger)
	require.NoError(t, err)

	mockProvider := mock.NewProvider(logger, &mock.Config{})
	srv.RegisterProvider("mock", mockProvider)

	// Get existing provider
	prov, err := srv.getProvider("mock")
	assert.NoError(t, err)
	assert.NotNil(t, prov)

	// Get non-existent provider
	prov, err = srv.getProvider("nonexistent")
	assert.Error(t, err)
	assert.Nil(t, prov)
	assert.Contains(t, err.Error(), "not found")
}

// Helper function to start test server
func startTestServer(t *testing.T) (*Server, string, func()) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Use port 0 to get random available port
	cfg := &Config{
		AgentID:    "test-agent",
		ListenAddr: "localhost:0",
		UseTLS:     false,
	}

	srv, err := NewServer(cfg, logger)
	require.NoError(t, err)

	// Register mock provider
	mockProvider := mock.NewProvider(logger, &mock.Config{
		ProvisionDelay: 10 * time.Millisecond,
		DestroyDelay:   5 * time.Millisecond,
	})
	err = srv.RegisterProvider("mock", mockProvider)
	require.NoError(t, err)

	// Start server in background
	lis, err := net.Listen("tcp", cfg.ListenAddr)
	require.NoError(t, err)

	addr := lis.Addr().String()

	go func() {
		srv.grpcServer.Serve(lis)
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	cleanup := func() {
		srv.Stop()
	}

	return srv, addr, cleanup
}

func TestServer_Provision(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	// Create client
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	// Test provision
	req := &pb.ProvisionRequest{
		ProviderName: "mock",
		Spec: &pb.ResourceSpec{
			Type:         "vm",
			ProviderType: "mock",
			Image:        "test-image",
			Cpus:         2,
			MemoryMb:     2048,
			DiskGb:       20,
			Labels:       map[string]string{"env": "test"},
		},
	}

	resp, err := client.Provision(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Resource.Id)
	assert.Equal(t, "mock", resp.Resource.ProviderType)
}

func TestServer_Provision_ProviderNotFound(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	req := &pb.ProvisionRequest{
		ProviderName: "nonexistent",
		Spec: &pb.ResourceSpec{
			Type:  "vm",
			Image: "test-image",
		},
	}

	resp, err := client.Provision(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not found")
}

func TestServer_Destroy(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	// First provision a resource
	provReq := &pb.ProvisionRequest{
		ProviderName: "mock",
		Spec: &pb.ResourceSpec{
			Type:  "vm",
			Image: "test-image",
		},
	}

	provResp, err := client.Provision(ctx, provReq)
	require.NoError(t, err)
	require.NotNil(t, provResp)

	// Now destroy it
	destroyReq := &pb.DestroyRequest{
		ProviderName: "mock",
		Resource:     provResp.Resource,
	}

	destroyResp, err := client.Destroy(ctx, destroyReq)
	assert.NoError(t, err)
	assert.NotNil(t, destroyResp)
	assert.True(t, destroyResp.Success)
}

func TestServer_GetStatus(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	// Provision a resource
	provResp, err := client.Provision(ctx, &pb.ProvisionRequest{
		ProviderName: "mock",
		Spec:         &pb.ResourceSpec{Type: "vm", Image: "test-image"},
	})
	require.NoError(t, err)

	// Get status
	statusReq := &pb.GetStatusRequest{
		ProviderName: "mock",
		Resource:     provResp.Resource,
	}

	statusResp, err := client.GetStatus(ctx, statusReq)
	assert.NoError(t, err)
	assert.NotNil(t, statusResp)
	assert.NotNil(t, statusResp.Status)
	assert.NotEmpty(t, statusResp.Status.State)
}

func TestServer_GetConnectionInfo(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	// Provision a resource
	provResp, err := client.Provision(ctx, &pb.ProvisionRequest{
		ProviderName: "mock",
		Spec:         &pb.ResourceSpec{Type: "vm", Image: "test-image"},
	})
	require.NoError(t, err)

	// Get connection info
	connReq := &pb.GetConnectionInfoRequest{
		ProviderName: "mock",
		Resource:     provResp.Resource,
	}

	connResp, err := client.GetConnectionInfo(ctx, connReq)
	assert.NoError(t, err)
	assert.NotNil(t, connResp)
	assert.NotNil(t, connResp.ConnectionInfo)
}

func TestServer_HealthCheck(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	// Health check for existing provider
	req := &pb.HealthCheckRequest{
		ProviderName: "mock",
	}

	resp, err := client.HealthCheck(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Healthy)

	// Health check for non-existent provider
	req.ProviderName = "nonexistent"
	resp, err = client.HealthCheck(ctx, req)
	assert.NoError(t, err) // Should return response, not error
	assert.NotNil(t, resp)
	assert.False(t, resp.Healthy)
	assert.Contains(t, resp.Message, "not found")
}

func TestServer_Exec(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	// Provision a resource
	provResp, err := client.Provision(ctx, &pb.ProvisionRequest{
		ProviderName: "mock",
		Spec:         &pb.ResourceSpec{Type: "vm", Image: "test-image"},
	})
	require.NoError(t, err)

	// Execute command
	execReq := &pb.ExecRequest{
		ProviderName: "mock",
		Resource:     provResp.Resource,
		Command:      []string{"echo", "hello"},
	}

	execResp, err := client.Exec(ctx, execReq)
	assert.NoError(t, err)
	assert.NotNil(t, execResp)
	assert.Equal(t, int32(0), execResp.ExitCode)
}

func TestServer_Update(t *testing.T) {
	_, addr, cleanup := startTestServer(t)
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	// Provision a resource
	provResp, err := client.Provision(ctx, &pb.ProvisionRequest{
		ProviderName: "mock",
		Spec:         &pb.ResourceSpec{Type: "vm", Image: "test-image"},
	})
	require.NoError(t, err)

	// Update resource
	updateReq := &pb.UpdateRequest{
		ProviderName: "mock",
		Resource:     provResp.Resource,
		Action:       "power-running",
		Params:       map[string]string{},
	}

	updateResp, err := client.Update(ctx, updateReq)
	assert.NoError(t, err)
	assert.NotNil(t, updateResp)
	assert.True(t, updateResp.Success)
}

func BenchmarkServer_Provision(b *testing.B) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	cfg := &Config{
		AgentID:    "bench-agent",
		ListenAddr: "localhost:0",
		UseTLS:     false,
	}

	srv, err := NewServer(cfg, logger)
	require.NoError(b, err)

	mockProvider := mock.NewProvider(logger, &mock.Config{
		ProvisionDelay: 1 * time.Millisecond,
	})
	srv.RegisterProvider("mock", mockProvider)

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	require.NoError(b, err)

	go srv.grpcServer.Serve(lis)
	defer srv.Stop()

	time.Sleep(50 * time.Millisecond)

	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(b, err)
	defer conn.Close()

	client := pb.NewProviderServiceClient(conn)
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req := &pb.ProvisionRequest{
			ProviderName: "mock",
			Spec: &pb.ResourceSpec{
				Type:  "vm",
				Image: fmt.Sprintf("image-%d", i),
			},
		}

		_, err := client.Provision(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}
