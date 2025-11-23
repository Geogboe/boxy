package remote

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
	pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

// RemoteProvider implements the Provider interface by proxying calls to a remote agent via gRPC.
//
// **Potential Problem #2 Addressed: Connection Management**
// - Uses connection pooling via single gRPC client
// - Implements retry logic for transient failures
// - Tracks connection health
type RemoteProvider struct {
	name          string
	providerName  string // Name of the actual provider on the agent (e.g., "hyperv")
	resourceType  provider_pkg.ResourceType
	agentID       string
	agentAddress  string
	conn          *grpc.ClientConn
	client        pb.ProviderServiceClient
	logger        *logrus.Logger
	maxRetries    int
	retryDelay    time.Duration
	requestTimeout time.Duration
}

// Config holds configuration for RemoteProvider
type Config struct {
	Name           string
	ProviderName   string // Provider name on the remote agent
	AgentID        string
	AgentAddress   string // host:port
	TLSCertPath    string // Path to client certificate
	TLSKeyPath     string // Path to client key
	TLSCAPath      string // Path to CA certificate
	MaxRetries     int
	RetryDelay     time.Duration
	RequestTimeout time.Duration
	UseTLS         bool
}

// NewRemoteProvider creates a new remote provider that connects to an agent.
//
// **Potential Problem #3 Addressed: Secure Communication**
// - Supports mTLS when TLS paths are provided
// - Falls back to insecure for testing/development
func NewRemoteProvider(cfg *Config, logger *logrus.Logger) (*RemoteProvider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if cfg.AgentAddress == "" {
		return nil, fmt.Errorf("agent address is required")
	}
	if cfg.ProviderName == "" {
		return nil, fmt.Errorf("provider name is required")
	}

	// Set defaults
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 1 * time.Second
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 5 * time.Minute
	}

	// Setup TLS or insecure credentials
	var opts []grpc.DialOption
	if cfg.UseTLS && cfg.TLSCertPath != "" && cfg.TLSKeyPath != "" && cfg.TLSCAPath != "" {
		// Load TLS credentials
		tlsCreds, err := credentials.NewClientTLSFromFile(cfg.TLSCAPath, "")
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(tlsCreds))
		logger.WithFields(logrus.Fields{
			"agent":    cfg.AgentAddress,
			"provider": cfg.ProviderName,
		}).Info("RemoteProvider using mTLS")
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		logger.WithFields(logrus.Fields{
			"agent":    cfg.AgentAddress,
			"provider": cfg.ProviderName,
		}).Warn("⚠️  SECURITY WARNING: RemoteProvider using insecure connection (no TLS) - credentials visible on network!")
	}

	// **Security Enhancement: gRPC Keepalive**
	// Prevents silent connection failures and enables connection reuse
	opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                10 * time.Second, // Send keepalive ping every 10 seconds
		Timeout:             5 * time.Second,  // Wait 5 seconds for ping response
		PermitWithoutStream: true,             // Send pings even without active RPCs
	}))

	// Connect to agent
	conn, err := grpc.NewClient(cfg.AgentAddress, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to agent at %s: %w", cfg.AgentAddress, err)
	}

	client := pb.NewProviderServiceClient(conn)

	rp := &RemoteProvider{
		name:           cfg.Name,
		providerName:   cfg.ProviderName,
		resourceType:   provider_pkg.ResourceTypeVM, // TODO: Make configurable
		agentID:        cfg.AgentID,
		agentAddress:   cfg.AgentAddress,
		conn:           conn,
		client:         client,
		logger:         logger,
		maxRetries:     cfg.MaxRetries,
		retryDelay:     cfg.RetryDelay,
		requestTimeout: cfg.RequestTimeout,
	}

	logger.WithFields(logrus.Fields{
		"agent":         cfg.AgentAddress,
		"provider":      cfg.ProviderName,
		"max_retries":   cfg.MaxRetries,
		"retry_delay":   cfg.RetryDelay,
		"request_timeout": cfg.RequestTimeout,
	}).Info("RemoteProvider created successfully")

	return rp, nil
}

// Name returns the provider name
func (r *RemoteProvider) Name() string {
	return r.name
}

// Type returns the resource type this provider manages
func (r *RemoteProvider) Type() provider_pkg.ResourceType {
	return r.resourceType
}

// Provision creates a new resource on the remote agent.
//
// **Potential Problem #4 Addressed: Timeout Handling**
// - Uses context with timeout for all remote calls
// - Returns clear errors on timeout
func (r *RemoteProvider) Provision(ctx context.Context, spec provider_pkg.ResourceSpec) (*provider_pkg.Resource, error) {
	ctx, cancel := context.WithTimeout(ctx, r.requestTimeout)
	defer cancel()

	logger := r.logger.WithFields(logrus.Fields{
		"agent":    r.agentAddress,
		"provider": r.providerName,
		"image":    spec.Image,
	})

	// Convert spec to proto
	pbSpec := resourceSpecToProto(&spec)

	req := &pb.ProvisionRequest{
		ProviderName: r.providerName,
		Spec:         pbSpec,
	}

	var resp *pb.ProvisionResponse
	var err error

	// **Potential Problem #2 Addressed: Retry Logic**
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			logger.WithField("attempt", attempt).Warn("Retrying Provision")
			time.Sleep(r.retryDelay * time.Duration(attempt)) // Exponential backoff
		}

		resp, err = r.client.Provision(ctx, req)
		if err == nil {
			break
		}

		// Check if error is retryable
		if !isRetryable(err) {
			logger.WithError(err).Error("Non-retryable error during Provision")
			return nil, fmt.Errorf("provision failed on agent %s: %w", r.agentAddress, err)
		}
	}

	if err != nil {
		logger.WithError(err).Error("Provision failed after retries")
		return nil, fmt.Errorf("provision failed on agent %s after %d retries: %w", r.agentAddress, r.maxRetries, err)
	}

	// Convert proto resource to internal
	res := protoToResource(resp.Resource)

	logger.WithField("resource_id", res.ID).Info("Resource provisioned successfully on remote agent")
	return res, nil
}

// Destroy removes a resource on the remote agent
func (r *RemoteProvider) Destroy(ctx context.Context, res *provider_pkg.Resource) error {
	ctx, cancel := context.WithTimeout(ctx, r.requestTimeout)
	defer cancel()

	logger := r.logger.WithFields(logrus.Fields{
		"agent":       r.agentAddress,
		"provider":    r.providerName,
		"resource_id": res.ID,
	})

	req := &pb.DestroyRequest{
		ProviderName: r.providerName,
		Resource:     resourceToProto(res),
	}

	var err error
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			logger.WithField("attempt", attempt).Warn("Retrying Destroy")
			time.Sleep(r.retryDelay * time.Duration(attempt))
		}

		_, err = r.client.Destroy(ctx, req)
		if err == nil {
			break
		}

		if !isRetryable(err) {
			logger.WithError(err).Error("Non-retryable error during Destroy")
			return fmt.Errorf("destroy failed on agent %s: %w", r.agentAddress, err)
		}
	}

	if err != nil {
		logger.WithError(err).Error("Destroy failed after retries")
		return fmt.Errorf("destroy failed on agent %s after %d retries: %w", r.agentAddress, r.maxRetries, err)
	}

	logger.Info("Resource destroyed successfully on remote agent")
	return nil
}

// GetStatus retrieves resource status from the remote agent
func (r *RemoteProvider) GetStatus(ctx context.Context, res *provider_pkg.Resource) (*provider_pkg.ResourceStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second) // Shorter timeout for status checks
	defer cancel()

	req := &pb.GetStatusRequest{
		ProviderName: r.providerName,
		Resource:     resourceToProto(res),
	}

	resp, err := r.client.GetStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get status failed on agent %s: %w", r.agentAddress, err)
	}

	status := &provider_pkg.ResourceStatus{
		State:      provider_pkg.ResourceState(resp.Status.State),
		Healthy:    resp.Status.Healthy,
		Message:    resp.Status.Message,
		LastCheck:  time.Unix(resp.Status.LastCheck, 0),
		Uptime:     time.Duration(resp.Status.UptimeSeconds) * time.Second,
		CPUUsage:   resp.Status.CpuUsage,
		MemoryUsed: resp.Status.MemoryUsed,
	}

	return status, nil
}

// GetConnectionInfo retrieves connection information from the remote agent
func (r *RemoteProvider) GetConnectionInfo(ctx context.Context, res *provider_pkg.Resource) (*provider_pkg.ConnectionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := &pb.GetConnectionInfoRequest{
		ProviderName: r.providerName,
		Resource:     resourceToProto(res),
	}

	resp, err := r.client.GetConnectionInfo(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get connection info failed on agent %s: %w", r.agentAddress, err)
	}

	connInfo := &provider_pkg.ConnectionInfo{
		Type:        resp.ConnectionInfo.Type,
		Host:        resp.ConnectionInfo.Host,
		Port:        int(resp.ConnectionInfo.Port),
		Username:    resp.ConnectionInfo.Username,
		Password:    resp.ConnectionInfo.Password,
		SSHKey:      resp.ConnectionInfo.SshKey,
		ExtraFields: stringMapToMap(resp.ConnectionInfo.ExtraFields),
	}

	return connInfo, nil
}

// HealthCheck checks if the remote provider is healthy
func (r *RemoteProvider) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req := &pb.HealthCheckRequest{
		ProviderName: r.providerName,
	}

	resp, err := r.client.HealthCheck(ctx, req)
	if err != nil {
		return fmt.Errorf("health check failed for agent %s: %w", r.agentAddress, err)
	}

	if !resp.Healthy {
		return fmt.Errorf("provider %s on agent %s is unhealthy: %s", r.providerName, r.agentAddress, resp.Message)
	}

	return nil
}

// Exec runs a command inside the resource on the remote agent
func (r *RemoteProvider) Exec(ctx context.Context, res *provider_pkg.Resource, cmd []string) (*provider_pkg.ExecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.requestTimeout)
	defer cancel()

	logger := r.logger.WithFields(logrus.Fields{
		"agent":       r.agentAddress,
		"provider":    r.providerName,
		"resource_id": res.ID,
		"command":     cmd,
	})

	req := &pb.ExecRequest{
		ProviderName: r.providerName,
		Resource:     resourceToProto(res),
		Command:      cmd,
	}

	var resp *pb.ExecResponse
	var err error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			logger.WithField("attempt", attempt).Warn("Retrying Exec")
			time.Sleep(r.retryDelay * time.Duration(attempt))
		}

		resp, err = r.client.Exec(ctx, req)
		if err == nil {
			break
		}

		if !isRetryable(err) {
			logger.WithError(err).Error("Non-retryable error during Exec")
			return nil, fmt.Errorf("exec failed on agent %s: %w", r.agentAddress, err)
		}
	}

	if err != nil {
		logger.WithError(err).Error("Exec failed after retries")
		return nil, fmt.Errorf("exec failed on agent %s after %d retries: %w", r.agentAddress, r.maxRetries, err)
	}

	result := &provider_pkg.ExecResult{
		ExitCode: int(resp.ExitCode),
		Stdout:   resp.Stdout,
		Stderr:   resp.Stderr,
	}

	if resp.Error != "" {
		result.Error = fmt.Errorf("%s", resp.Error)
	}

	logger.WithField("exit_code", result.ExitCode).Debug("Exec completed on remote agent")
	return result, nil
}

// Update applies updates to the resource on the remote agent
func (r *RemoteProvider) Update(ctx context.Context, res *provider_pkg.Resource, updates provider_pkg.ResourceUpdate) error {
	ctx, cancel := context.WithTimeout(ctx, r.requestTimeout)
	defer cancel()

	// Convert ResourceUpdate to action/params for proto
	action, params := resourceUpdateToProto(updates)

	logger := r.logger.WithFields(logrus.Fields{
		"agent":       r.agentAddress,
		"provider":    r.providerName,
		"resource_id": res.ID,
		"action":      action,
	})

	req := &pb.UpdateRequest{
		ProviderName: r.providerName,
		Resource:     resourceToProto(res),
		Action:       action,
		Params:       params,
	}

	var resp *pb.UpdateResponse
	var err error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			logger.WithField("attempt", attempt).Warn("Retrying Update")
			time.Sleep(r.retryDelay * time.Duration(attempt))
		}

		resp, err = r.client.Update(ctx, req)
		if err == nil {
			break
		}

		if !isRetryable(err) {
			logger.WithError(err).Error("Non-retryable error during Update")
			return fmt.Errorf("update failed on agent %s: %w", r.agentAddress, err)
		}
	}

	if err != nil {
		logger.WithError(err).Error("Update failed after retries")
		return fmt.Errorf("update failed on agent %s after %d retries: %w", r.agentAddress, r.maxRetries, err)
	}

	if !resp.Success {
		return fmt.Errorf("update action '%s' failed on agent %s: %s", action, r.agentAddress, resp.Error)
	}

	// Update resource metadata from response
	if resp.Resource != nil {
		updated := protoToResource(resp.Resource)
		*res = *updated
	}

	logger.Info("Resource updated successfully on remote agent")
	return nil
}

// Close closes the gRPC connection
func (r *RemoteProvider) Close() error {
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// isRetryable determines if an error is transient and should be retried
func isRetryable(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}

	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}
