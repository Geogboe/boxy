package runtime

import (
	"context"
	"runtime"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/docker"
	"github.com/Geogboe/boxy/pkg/provider/hyperv"
	"github.com/Geogboe/boxy/pkg/provider/scratch/shell"
)

// BuildRegistry constructs a provider registry from pool configs, skipping
// unavailable backends (e.g., docker daemon down).
func BuildRegistry(ctx context.Context, pools []pool.PoolConfig, logger *logrus.Logger, encryptor *crypto.Encryptor) *provider.Registry {
	registry := provider.NewRegistry()

	registerDockerIfAvailable(ctx, registry, logger, encryptor)

	if needsScratchShell(pools) {
		registry.Register("scratch/shell", shell.New(logger, scratchShellConfigFromPools(pools)))
		logger.Info("scratch/shell provider registered")
	}

	if runtime.GOOS == "windows" && poolsNeedBackend(pools, "hyperv") {
		registry.Register("hyperv", hyperv.NewProvider(logger, encryptor))
		logger.Info("Hyper-V provider registered")
	}

	return registry
}

func registerDockerIfAvailable(ctx context.Context, registry *provider.Registry, logger *logrus.Logger, encryptor *crypto.Encryptor) {
	prov, err := docker.NewProvider(logger, encryptor)
	if err != nil {
		logger.WithError(err).Info("Docker provider unavailable")
		return
	}
	hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := prov.HealthCheck(hctx); err != nil {
		logger.WithError(err).Info("Docker health check failed; skipping docker provider")
		return
	}
	registry.Register("docker", prov)
	logger.Info("Docker provider registered")
}

func needsScratchShell(pools []pool.PoolConfig) bool {
	return poolsNeedBackend(pools, "scratch/shell")
}

func poolsNeedBackend(pools []pool.PoolConfig, backend string) bool {
	for _, p := range pools {
		if p.Backend == backend {
			return true
		}
	}
	return false
}

func scratchShellConfigFromPools(pools []pool.PoolConfig) shell.Config {
	cfg := shell.Config{}
	for _, p := range pools {
		if p.Backend != "scratch/shell" {
			continue
		}
		if base, ok := p.ExtraConfig["base_dir"].(string); ok {
			cfg.BaseDir = base
		}
		if shells := readStringSlice(p.ExtraConfig["allowed_shells"]); len(shells) > 0 {
			cfg.AllowedShells = shells
		}
		if minFree, ok := readUint64(p.ExtraConfig["min_free_bytes"]); ok {
			cfg.MinFreeBytes = minFree
		}
		break
	}
	return cfg
}

func readStringSlice(val interface{}) []string {
	switch v := val.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func readUint64(val interface{}) (uint64, bool) {
	switch v := val.(type) {
	case int:
		if v > 0 {
			return uint64(v), true
		}
	case int64:
		if v > 0 {
			return uint64(v), true
		}
	case float64:
		if v > 0 {
			return uint64(v), true
		}
	}
	return 0, false
}
