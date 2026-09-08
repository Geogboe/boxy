package pool

import "errors"

// DescribeJobError maps one of this package's own typed pool-operation
// errors to a stable, safe diagnostics error_code plus an operator-facing
// summary derived from the error's own message. It recognizes only pool's
// typed errors and returns ("", "") for anything else so a caller can fall
// back to its own generic classification (e.g. pkg/diagnostics.DescribeError)
// without this ever silently overriding a driver/transport-level code that
// classification already produces.
//
// This exists so "a pool operation failed because quarantined (Error-state)
// resources already consume the pool's max_total" is labeled identically
// (error_code "quarantine_exhausted") wherever it surfaces: the async
// pool-job path (internal/server/api_pools.go's poolJobErrorCode) and the
// periodic background reconcile loop's diagnostics logging
// (internal/cli/serve_ui.go's reconcileError). Before #328 these were two
// independent classifiers that could drift; this is the single source of
// truth per AGENTS.md's DRY value.
func DescribeJobError(err error) (code, summary string) {
	if err == nil {
		return "", ""
	}
	var blocked *BlockedPoolError
	if errors.As(err, &blocked) {
		return "quarantine_exhausted", blocked.Error()
	}
	var drained *ConfigDeclaredDrainError
	if errors.As(err, &drained) {
		return "pool_config_drained", drained.Error()
	}
	var maxTotal *MaxTotalReachedError
	if errors.As(err, &maxTotal) {
		return "pool_max_total_reached", maxTotal.Error()
	}
	var drainedPool *DrainedPoolError
	if errors.As(err, &drainedPool) {
		return "pool_drained", drainedPool.Error()
	}
	return "", ""
}
