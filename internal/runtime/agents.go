package runtime

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/agent"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/docker"
	"github.com/Geogboe/boxy/pkg/provider/hyperv"
	"github.com/Geogboe/boxy/pkg/provider/mock"
	"github.com/Geogboe/boxy/pkg/provider/remote"
	"github.com/Geogboe/boxy/pkg/provider/scratch/shell"
)

// registerRemoteAgents adds remote providers from config into the registry.
func registerRemoteAgents(ctx context.Context, cfg *config.Config, logger *logrus.Logger, registry *provider.Registry) {
	for _, agentCfg := range cfg.Agents {
		logger.WithFields(logrus.Fields{
			"agent_id":  agentCfg.ID,
			"address":   agentCfg.Address,
			"providers": agentCfg.Providers,
		}).Info("Registering remote agent")

		for _, provName := range agentCfg.Providers {
			remoteCfg := &remote.Config{
				Name:         fmt.Sprintf("%s-%s", agentCfg.ID, provName),
				ProviderName: provName,
				AgentID:      agentCfg.ID,
				AgentAddress: agentCfg.Address,
				TLSCertPath:  agentCfg.TLSCertPath,
				TLSKeyPath:   agentCfg.TLSKeyPath,
				TLSCAPath:    agentCfg.TLSCAPath,
				UseTLS:       agentCfg.UseTLS,
			}

			remoteProv, err := remote.NewRemoteProvider(remoteCfg, logger)
			if err != nil {
				logger.WithError(err).WithFields(logrus.Fields{
					"agent":    agentCfg.ID,
					"provider": provName,
				}).Error("Failed to create remote provider, skipping")
				continue
			}

			registry.Register(remoteCfg.Name, remoteProv)
			logger.WithFields(logrus.Fields{
				"provider_name": remoteCfg.Name,
				"agent":         agentCfg.ID,
				"backend":       provName,
			}).Info("Remote provider registered")

			hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := remoteProv.HealthCheck(hctx); err != nil {
				logger.WithError(err).WithField("provider", remoteCfg.Name).Warn("Remote provider health check failed")
			} else {
				logger.WithField("provider", remoteCfg.Name).Info("Remote provider is healthy")
			}
			cancel()
		}
	}
}

// AgentBootstrap builds and registers providers for the agent server based on flags.
func AgentBootstrap(ctx context.Context, srv *agent.Server, logger *logrus.Logger, encryptor *crypto.Encryptor, providersToEnable []string) {
	for _, provName := range providersToEnable {
		switch provName {
		case "docker":
			prov, err := docker.NewProvider(logger, encryptor)
			if err != nil {
				logger.WithError(err).Info("Docker provider unavailable, skipping")
				continue
			}
			hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			if err := prov.HealthCheck(hctx); err != nil {
				cancel()
				logger.WithError(err).Info("Docker health check failed, skipping docker provider")
				continue
			}
			cancel()
			if err := srv.RegisterProvider("docker", prov); err != nil {
				logger.WithError(err).WithField("provider", "docker").Error("Failed to register docker provider")
				continue
			}
			logger.Info("Registered Docker provider")

		case "hyperv":
			if runtime.GOOS != "windows" {
				logger.Warn("Hyper-V provider requires Windows, skipping")
				continue
			}
			prov := hyperv.NewProvider(logger, encryptor)
			if err := srv.RegisterProvider("hyperv", prov); err != nil {
				logger.WithError(err).WithField("provider", "hyperv").Error("Failed to register hyperv provider")
				continue
			}
			logger.Info("Registered Hyper-V provider")

		case "mock":
			mockCfg := &mock.Config{
				ProvisionDelay: 2 * time.Second,
				DestroyDelay:   1 * time.Second,
			}
			prov := mock.NewProvider(logger, mockCfg)
			if err := srv.RegisterProvider("mock", prov); err != nil {
				logger.WithError(err).WithField("provider", "mock").Error("Failed to register mock provider")
				continue
			}
			logger.Info("Registered Mock provider")

		case "scratch/shell":
			prov := shell.New(logger, shell.Config{})
			if err := srv.RegisterProvider(prov.Name(), prov); err != nil {
				logger.WithError(err).WithField("provider", prov.Name()).Error("Failed to register scratch/shell provider")
				continue
			}
			logger.Info("Registered scratch/shell provider")

		default:
			logger.WithField("provider", provName).Warn("Unknown provider, skipping")
		}
	}
}
