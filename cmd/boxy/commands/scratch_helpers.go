package commands

import (
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/pkg/provider/scratch/shell"
)

// needsScratchShell returns true if any pool references the scratch/shell backend.
func needsScratchShell(pools []pool.PoolConfig) bool {
	for _, p := range pools {
		if p.Backend == "scratch/shell" {
			return true
		}
	}
	return false
}

// scratchShellConfigFromPools extracts optional config for scratch/shell from the first matching pool.
// Supported ExtraConfig keys:
// - base_dir (string)
// - allowed_shells ([]string or []interface{} of strings)
// - min_free_bytes (int/int64/float64)
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
