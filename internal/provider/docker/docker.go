package docker

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/crypto"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
)

// Provider implements the provider.Provider interface for Docker
type Provider struct {
	client    *client.Client
	logger    *logrus.Logger
	encryptor *crypto.Encryptor
}

// NewProvider creates a new Docker provider
func NewProvider(logger *logrus.Logger, encryptor *crypto.Encryptor) (*Provider, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &Provider{
		client:    cli,
		logger:    logger,
		encryptor: encryptor,
	}, nil
}

// Provision creates a new Docker container
func (p *Provider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
	p.logger.WithFields(logrus.Fields{
		"image": spec.Image,
		"type":  spec.Type,
	}).Info("Provisioning Docker container")

	// Pull image if not present
	if err := p.ensureImage(ctx, spec.Image); err != nil {
		return nil, fmt.Errorf("failed to ensure image: %w", err)
	}

	// Generate random password for the container
	password := generatePassword(16)

	// Prepare container configuration
	config := &container.Config{
		Image: spec.Image,
		Env: []string{
			fmt.Sprintf("BOXY_PASSWORD=%s", password),
		},
		Labels: map[string]string{
			"boxy.managed": "true",
		},
		Tty: true, // Keep container running
	}

	// Add custom environment variables
	for k, v := range spec.Environment {
		config.Env = append(config.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Add custom labels
	for k, v := range spec.Labels {
		config.Labels[k] = v
	}

	// Host configuration for resource limits
	hostConfig := &container.HostConfig{}
	if spec.CPUs > 0 {
		hostConfig.NanoCPUs = int64(spec.CPUs * 1e9)
	}
	if spec.MemoryMB > 0 {
		hostConfig.Memory = int64(spec.MemoryMB * 1024 * 1024)
	}

	// Create container
	resp, err := p.client.ContainerCreate(
		ctx,
		config,
		hostConfig,
		&network.NetworkingConfig{},
		nil,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := p.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Cleanup on failure
		_ = p.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Inspect container to get details
	inspect, err := p.client.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	// Encrypt password before storing
	encryptedPassword, err := p.encryptor.Encrypt(password)
	if err != nil {
		// Cleanup container on encryption failure
		_ = p.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to encrypt password: %w", err)
	}

	// Create resource object
	res := &resource.Resource{
		Type:         resource.ResourceTypeContainer,
		State:        resource.StateReady,
		ProviderType: "docker",
		ProviderID:   resp.ID,
		Spec: map[string]interface{}{
			"image":       spec.Image,
			"cpus":        spec.CPUs,
			"memory_mb":   spec.MemoryMB,
			"labels":      spec.Labels,
			"environment": spec.Environment,
		},
		Metadata: map[string]interface{}{
			"container_name":     inspect.Name,
			"ip_address":         inspect.NetworkSettings.IPAddress,
			"password_encrypted": encryptedPassword, // Encrypted password
			"created":            inspect.Created,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	p.logger.WithFields(logrus.Fields{
		"container_id": resp.ID,
		"image":        spec.Image,
	}).Info("Container provisioned successfully")

	return res, nil
}

// Destroy removes a Docker container
func (p *Provider) Destroy(ctx context.Context, res *resource.Resource) error {
	p.logger.WithField("container_id", res.ProviderID).Info("Destroying container")

	// Remove container forcefully
	err := p.client.ContainerRemove(ctx, res.ProviderID, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	p.logger.WithField("container_id", res.ProviderID).Info("Container destroyed successfully")
	return nil
}

// GetStatus returns the current status of a container
func (p *Provider) GetStatus(ctx context.Context, res *resource.Resource) (*resource.ResourceStatus, error) {
	inspect, err := p.client.ContainerInspect(ctx, res.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	status := &resource.ResourceStatus{
		Healthy:   inspect.State.Running,
		Message:   inspect.State.Status,
		LastCheck: time.Now(),
	}

	// Map Docker state to resource state
	if inspect.State.Running {
		status.State = resource.StateReady
	} else if inspect.State.Dead {
		status.State = resource.StateError
	} else {
		status.State = resource.StateProvisioning
	}

	// Get stats (optional, can be heavy)
	stats, err := p.client.ContainerStats(ctx, res.ProviderID, false)
	if err == nil {
		defer stats.Body.Close()
		// Parse stats if needed (currently just acknowledging it works)
		io.Copy(io.Discard, stats.Body)
	}

	return status, nil
}

// GetConnectionInfo returns connection details for a container
func (p *Provider) GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error) {
	inspect, err := p.client.ContainerInspect(ctx, res.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	// Decrypt password from metadata
	encryptedPassword, _ := res.Metadata["password_encrypted"].(string)
	password, err := p.encryptor.Decrypt(encryptedPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt password: %w", err)
	}

	connInfo := &resource.ConnectionInfo{
		Type:     "docker-exec",
		Host:     inspect.NetworkSettings.IPAddress,
		Username: "root",
		Password: password,
		ExtraFields: map[string]interface{}{
			"container_id":   res.ProviderID,
			"container_name": inspect.Name,
		},
	}

	// Add exposed ports
	if len(inspect.NetworkSettings.Ports) > 0 {
		ports := make(map[string]string)
		for portNat, bindings := range inspect.NetworkSettings.Ports {
			if len(bindings) > 0 {
				ports[string(portNat)] = bindings[0].HostPort
			}
		}
		connInfo.ExtraFields["ports"] = ports
	}

	return connInfo, nil
}

// HealthCheck verifies Docker daemon is accessible
func (p *Provider) HealthCheck(ctx context.Context) error {
	_, err := p.client.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker daemon not reachable: %w", err)
	}
	return nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "docker"
}

// Type returns the resource type this provider handles
func (p *Provider) Type() resource.ResourceType {
	return resource.ResourceTypeContainer
}

// ensureImage pulls the image if it doesn't exist locally
func (p *Provider) ensureImage(ctx context.Context, image string) error {
	// Check if image exists
	_, _, err := p.client.ImageInspectWithRaw(ctx, image)
	if err == nil {
		// Image exists
		return nil
	}

	p.logger.WithField("image", image).Info("Pulling Docker image")

	// Pull image
	reader, err := p.client.ImagePull(ctx, image, imagetypes.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()

	// Drain the reader (pull progress)
	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return fmt.Errorf("failed to complete image pull: %w", err)
	}

	p.logger.WithField("image", image).Info("Image pulled successfully")
	return nil
}

// generatePassword generates a cryptographically secure random password
func generatePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	b := make([]byte, length)

	// Use crypto/rand for cryptographically secure random numbers
	randomBytes := make([]byte, length)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to timestamp-based if crypto fails (should never happen)
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}

	for i := range b {
		b[i] = charset[int(randomBytes[i])%len(charset)]
	}
	return string(b)
}

// Update modifies a resource (for Docker: resource limits, pause/unpause)
func (p *Provider) Update(ctx context.Context, res *resource.Resource, updates provider_pkg.ResourceUpdate) error {
	// TODO(mvp2): Implement resource limit updates
	// For now, return not supported
	return fmt.Errorf("Update not yet implemented for Docker provider")
}

// Execute runs a command inside the container
func (p *Provider) Exec(ctx context.Context, res *resource.Resource, cmd []string) (*provider_pkg.ExecResult, error) {
	p.logger.WithFields(logrus.Fields{
		"container_id": res.ProviderID,
		"command":      cmd,
	}).Debug("Executing command in container")

	// Create exec instance
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}

	execID, err := p.client.ContainerExecCreate(ctx, res.ProviderID, execConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec: %w", err)
	}

	// Attach and run
	resp, err := p.client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach exec: %w", err)
	}
	defer resp.Close()

	// Read output
	var stdout, stderr strings.Builder
	_, err = stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read exec output: %w", err)
	}

	// Get exit code
	inspect, err := p.client.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect exec: %w", err)
	}

	result := &provider_pkg.ExecResult{
		ExitCode: inspect.ExitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}

	p.logger.WithFields(logrus.Fields{
		"container_id": res.ProviderID,
		"exit_code":    result.ExitCode,
	}).Debug("Command execution completed")

	return result, nil
}

// Ensure Provider implements provider.Provider interface
var _ provider_pkg.Provider = (*Provider)(nil)
