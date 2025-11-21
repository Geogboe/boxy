package agent

import (
	"context"
	"fmt"
	"net"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
	pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

// Server implements the gRPC ProviderService interface.
// It runs on remote machines (e.g., Windows with Hyper-V) and routes
// RPC calls to local provider implementations.
//
// **Potential Problem #6 Addressed: Provider Routing**
// - Maps provider names to local provider instances
// - Validates providers exist before forwarding calls
type Server struct {
	pb.UnimplementedProviderServiceServer

	agentID    string
	address    string
	providers  map[string]provider_pkg.Provider // provider name -> provider instance
	logger     *logrus.Logger
	grpcServer *grpc.Server
}

// Config holds agent server configuration
type Config struct {
	AgentID      string
	ListenAddr   string // host:port to listen on
	TLSCertPath  string // Path to server certificate
	TLSKeyPath   string // Path to server key
	TLSCAPath    string // Path to CA certificate (for client verification)
	UseTLS       bool
}

// NewServer creates a new agent server
func NewServer(cfg *Config, logger *logrus.Logger) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if cfg.ListenAddr == "" {
		return nil, fmt.Errorf("listen address is required")
	}
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("agent ID is required")
	}

	s := &Server{
		agentID:   cfg.AgentID,
		address:   cfg.ListenAddr,
		providers: make(map[string]provider_pkg.Provider),
		logger:    logger,
	}

	// Setup gRPC server with TLS or insecure
	var opts []grpc.ServerOption
	if cfg.UseTLS && cfg.TLSCertPath != "" && cfg.TLSKeyPath != "" {
		tlsCreds, err := credentials.NewServerTLSFromFile(cfg.TLSCertPath, cfg.TLSKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		opts = append(opts, grpc.Creds(tlsCreds))
		logger.Info("Agent server using mTLS")
	} else {
		opts = append(opts, grpc.Creds(insecure.NewCredentials()))
		logger.Warn("Agent server using insecure connection (no TLS)")
	}

	s.grpcServer = grpc.NewServer(opts...)
	pb.RegisterProviderServiceServer(s.grpcServer, s)

	logger.WithFields(logrus.Fields{
		"agent_id": cfg.AgentID,
		"address":  cfg.ListenAddr,
		"tls":      cfg.UseTLS,
	}).Info("Agent server created successfully")

	return s, nil
}

// RegisterProvider adds a provider to the server's registry
func (s *Server) RegisterProvider(name string, provider provider_pkg.Provider) error {
	if _, exists := s.providers[name]; exists {
		return fmt.Errorf("provider %s already registered", name)
	}

	s.providers[name] = provider
	s.logger.WithField("provider", name).Info("Provider registered with agent")
	return nil
}

// Start starts the gRPC server
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.address, err)
	}

	s.logger.WithFields(logrus.Fields{
		"agent_id":  s.agentID,
		"address":   s.address,
		"providers": s.getProviderNames(),
	}).Info("Agent server starting...")

	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	s.logger.Info("Agent server stopping...")
	s.grpcServer.GracefulStop()
}

// **Potential Problem #7 Addressed: Provider Lookup**
// Helper to get provider by name with validation
func (s *Server) getProvider(name string) (provider_pkg.Provider, error) {
	prov, exists := s.providers[name]
	if !exists {
		return nil, status.Errorf(codes.NotFound, "provider %s not found on agent %s", name, s.agentID)
	}
	return prov, nil
}

func (s *Server) getProviderNames() []string {
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	return names
}

// **ProviderService RPC Implementations**

// Provision creates a new resource using the specified provider
func (s *Server) Provision(ctx context.Context, req *pb.ProvisionRequest) (*pb.ProvisionResponse, error) {
	logger := s.logger.WithFields(logrus.Fields{
		"provider": req.ProviderName,
		"image":    req.Spec.Image,
	})

	logger.Info("Provision request received")

	prov, err := s.getProvider(req.ProviderName)
	if err != nil {
		logger.WithError(err).Error("Provider not found")
		return nil, err
	}

	// Convert proto spec to internal spec
	spec := protoToResourceSpec(req.Spec)

	res, err := prov.Provision(ctx, spec)
	if err != nil {
		logger.WithError(err).Error("Provision failed")
		return nil, status.Errorf(codes.Internal, "provision failed: %v", err)
	}

	logger.WithField("resource_id", res.ID).Info("Resource provisioned successfully")

	return &pb.ProvisionResponse{
		Resource: resourceToProto(res),
	}, nil
}

// Destroy removes a resource
func (s *Server) Destroy(ctx context.Context, req *pb.DestroyRequest) (*pb.DestroyResponse, error) {
	logger := s.logger.WithFields(logrus.Fields{
		"provider":    req.ProviderName,
		"resource_id": req.Resource.Id,
	})

	logger.Info("Destroy request received")

	prov, err := s.getProvider(req.ProviderName)
	if err != nil {
		logger.WithError(err).Error("Provider not found")
		return nil, err
	}

	res := protoToResource(req.Resource)

	if err := prov.Destroy(ctx, res); err != nil {
		logger.WithError(err).Error("Destroy failed")
		return nil, status.Errorf(codes.Internal, "destroy failed: %v", err)
	}

	logger.Info("Resource destroyed successfully")

	return &pb.DestroyResponse{
		Success: true,
	}, nil
}

// GetStatus retrieves resource status
func (s *Server) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	prov, err := s.getProvider(req.ProviderName)
	if err != nil {
		return nil, err
	}

	res := protoToResource(req.Resource)

	st, err := prov.GetStatus(ctx, res)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get status failed: %v", err)
	}

	return &pb.GetStatusResponse{
		Status: &pb.ResourceStatus{
			State:         string(st.State),
			Healthy:       st.Healthy,
			Message:       st.Message,
			LastCheck:     st.LastCheck.Unix(),
			UptimeSeconds: int64(st.Uptime.Seconds()),
			CpuUsage:      st.CPUUsage,
			MemoryUsed:    st.MemoryUsed,
		},
	}, nil
}

// GetConnectionInfo retrieves connection information
func (s *Server) GetConnectionInfo(ctx context.Context, req *pb.GetConnectionInfoRequest) (*pb.GetConnectionInfoResponse, error) {
	prov, err := s.getProvider(req.ProviderName)
	if err != nil {
		return nil, err
	}

	res := protoToResource(req.Resource)

	connInfo, err := prov.GetConnectionInfo(ctx, res)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get connection info failed: %v", err)
	}

	return &pb.GetConnectionInfoResponse{
		ConnectionInfo: &pb.ConnectionInfo{
			Type:        connInfo.Type,
			Host:        connInfo.Host,
			Port:        int32(connInfo.Port),
			Username:    connInfo.Username,
			Password:    connInfo.Password,
			SshKey:      connInfo.SSHKey,
			ExtraFields: mapToStringMap(connInfo.ExtraFields),
		},
	}, nil
}

// HealthCheck verifies provider health
func (s *Server) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	prov, err := s.getProvider(req.ProviderName)
	if err != nil {
		return &pb.HealthCheckResponse{
			Healthy: false,
			Message: err.Error(),
		}, nil // Return success with unhealthy status, not an error
	}

	if err := prov.HealthCheck(ctx); err != nil {
		return &pb.HealthCheckResponse{
			Healthy: false,
			Message: err.Error(),
		}, nil
	}

	return &pb.HealthCheckResponse{
		Healthy: true,
		Message: "Provider is healthy",
	}, nil
}

// Exec runs a command inside a resource
func (s *Server) Exec(ctx context.Context, req *pb.ExecRequest) (*pb.ExecResponse, error) {
	logger := s.logger.WithFields(logrus.Fields{
		"provider":    req.ProviderName,
		"resource_id": req.Resource.Id,
		"command":     req.Command,
	})

	logger.Debug("Exec request received")

	prov, err := s.getProvider(req.ProviderName)
	if err != nil {
		logger.WithError(err).Error("Provider not found")
		return nil, err
	}

	res := protoToResource(req.Resource)

	result, err := prov.Exec(ctx, res, req.Command)
	if err != nil {
		logger.WithError(err).Error("Exec failed")
		return nil, status.Errorf(codes.Internal, "exec failed: %v", err)
	}

	resp := &pb.ExecResponse{
		ExitCode: int32(result.ExitCode),
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}

	if result.Error != nil {
		resp.Error = result.Error.Error()
	}

	logger.WithField("exit_code", result.ExitCode).Debug("Exec completed successfully")

	return resp, nil
}

// Update applies updates to a resource
func (s *Server) Update(ctx context.Context, req *pb.UpdateRequest) (*pb.UpdateResponse, error) {
	logger := s.logger.WithFields(logrus.Fields{
		"provider":    req.ProviderName,
		"resource_id": req.Resource.Id,
		"action":      req.Action,
	})

	logger.Info("Update request received")

	prov, err := s.getProvider(req.ProviderName)
	if err != nil {
		logger.WithError(err).Error("Provider not found")
		return nil, err
	}

	res := protoToResource(req.Resource)

	// Convert proto action/params to ResourceUpdate
	updates := protoToResourceUpdate(req.Action, req.Params)

	err = prov.Update(ctx, res, updates)
	if err != nil {
		logger.WithError(err).Error("Update failed")
		return &pb.UpdateResponse{
			Success: false,
			Error:   err.Error(),
		}, nil // Return success with error message, not gRPC error
	}

	logger.Info("Resource updated successfully")

	return &pb.UpdateResponse{
		Success:  true,
		Resource: resourceToProto(res),
	}, nil
}

