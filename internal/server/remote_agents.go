package server

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/remote"
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
