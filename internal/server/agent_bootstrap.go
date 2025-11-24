package server

import (
	"context"
	"runtime"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/agent"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider/docker"
	"github.com/Geogboe/boxy/pkg/provider/hyperv"
	"github.com/Geogboe/boxy/pkg/provider/mock"
	"github.com/Geogboe/boxy/pkg/provider/scratch/shell"
)

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
