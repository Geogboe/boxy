package hooks

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/provider"
)

// Executor executes hooks for resources
type Executor struct {
	logger *logrus.Logger
}

// NewExecutor creates a new hook executor
func NewExecutor(logger *logrus.Logger) *Executor {
	return &Executor{
		logger: logger,
	}
}

// ExecuteHooks executes a list of hooks for a resource
// Returns slice of results and error if any critical hook failed
func (e *Executor) ExecuteHooks(
	ctx context.Context,
	hooks []Hook,
	hookPoint HookPoint,
	prov provider.Provider,
	res *provider.Resource,
	hookCtx HookContext,
	phaseTimeout time.Duration,
) ([]HookResult, error) {
	if len(hooks) == 0 {
		e.logger.WithFields(logrus.Fields{
			"resource_id": res.ID,
			"hook_point":  hookPoint,
		}).Debug("No hooks to execute")
		return []HookResult{}, nil
	}

	e.logger.WithFields(logrus.Fields{
		"resource_id": res.ID,
		"hook_point":  hookPoint,
		"hook_count":  len(hooks),
	}).Info("Executing hooks")

	// Create context with phase timeout
	phaseCtx, phaseCancel := context.WithTimeout(ctx, phaseTimeout)
	defer phaseCancel()

	results := make([]HookResult, 0, len(hooks))

	for i, hook := range hooks {
		e.logger.WithFields(logrus.Fields{
			"resource_id": res.ID,
			"hook_name":   hook.Name,
			"hook_index":  i + 1,
			"hook_total":  len(hooks),
		}).Info("Executing hook")

		result, err := e.executeHookWithRetry(phaseCtx, hook, prov, res, hookCtx)
		results = append(results, result)

		if err != nil && !hook.ContinueOnFailure {
			e.logger.WithFields(logrus.Fields{
				"resource_id": res.ID,
				"hook_name":   hook.Name,
				"error":       err,
			}).Error("Hook failed (critical)")
			return results, fmt.Errorf("hook %s failed: %w", hook.Name, err)
		}

		if err != nil && hook.ContinueOnFailure {
			e.logger.WithFields(logrus.Fields{
				"resource_id": res.ID,
				"hook_name":   hook.Name,
				"error":       err,
			}).Warn("Hook failed (non-critical, continuing)")
		}

		// Check if phase timeout exceeded
		if phaseCtx.Err() != nil {
			return results, fmt.Errorf("phase timeout exceeded after executing %d/%d hooks", i+1, len(hooks))
		}
	}

	e.logger.WithFields(logrus.Fields{
		"resource_id": res.ID,
		"hook_point":  hookPoint,
	}).Info("All hooks executed successfully")

	return results, nil
}

// executeHookWithRetry executes a single hook with retry logic
func (e *Executor) executeHookWithRetry(
	ctx context.Context,
	hook Hook,
	prov provider.Provider,
	res *provider.Resource,
	hookCtx HookContext,
) (HookResult, error) {
	maxAttempts := 1
	if hook.Retry > 0 {
		maxAttempts = hook.Retry + 1 // retry=3 means 1 initial + 3 retries = 4 total
	}

	var lastResult HookResult
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			e.logger.WithFields(logrus.Fields{
				"resource_id": res.ID,
				"hook_name":   hook.Name,
				"attempt":     attempt,
				"max_attempts": maxAttempts,
			}).Info("Retrying hook")

			// Brief delay between retries
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return lastResult, ctx.Err()
			}
		}

		result, err := e.executeHook(ctx, hook, prov, res, hookCtx, attempt)
		lastResult = result
		lastErr = err

		if err == nil {
			e.logger.WithFields(logrus.Fields{
				"resource_id": res.ID,
				"hook_name":   hook.Name,
				"duration":    result.Duration,
				"exit_code":   result.ExitCode,
			}).Info("Hook executed successfully")
			return result, nil
		}

		e.logger.WithFields(logrus.Fields{
			"resource_id": res.ID,
			"hook_name":   hook.Name,
			"attempt":     attempt,
			"error":       err,
		}).Warn("Hook attempt failed")
	}

	return lastResult, lastErr
}

// executeHook executes a single hook once
func (e *Executor) executeHook(
	ctx context.Context,
	hook Hook,
	prov provider.Provider,
	res *provider.Resource,
	hookCtx HookContext,
	attempt int,
) (HookResult, error) {
	start := time.Now()

	result := HookResult{
		Hook:    hook.Name,
		Attempt: attempt,
	}

	// Get script content
	script, err := e.getScriptContent(hook)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result, err
	}

	// Expand template variables
	expandedScript := ExpandTemplate(script, hookCtx)

	// Get shell command
	cmd, err := GetShellCommand(hook.Shell, expandedScript)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result, err
	}

	// Set timeout for this specific hook
	timeout := hook.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute // default individual hook timeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.logger.WithFields(logrus.Fields{
		"resource_id": res.ID,
		"hook_name":   hook.Name,
		"command":     cmd,
		"timeout":     timeout,
	}).Debug("Executing command via provider.Exec()")

	// Execute via provider
	execResult, err := prov.Exec(execCtx, res, cmd)
	result.Duration = time.Since(start)

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		if execResult != nil {
			result.ExitCode = execResult.ExitCode
			result.Stdout = execResult.Stdout
			result.Stderr = execResult.Stderr
		}
		return result, fmt.Errorf("execution failed: %w", err)
	}

	result.ExitCode = execResult.ExitCode
	result.Stdout = execResult.Stdout
	result.Stderr = execResult.Stderr

	if execResult.ExitCode != 0 {
		result.Success = false
		result.Error = fmt.Sprintf("non-zero exit code: %d", execResult.ExitCode)
		return result, fmt.Errorf("hook exited with code %d", execResult.ExitCode)
	}

	result.Success = true
	return result, nil
}

// getScriptContent returns the script content from inline or file
func (e *Executor) getScriptContent(hook Hook) (string, error) {
	if hook.Inline != "" {
		return hook.Inline, nil
	}

	if hook.Path != "" {
		content, err := os.ReadFile(hook.Path)
		if err != nil {
			return "", fmt.Errorf("failed to read script file %s: %w", hook.Path, err)
		}
		return string(content), nil
	}

	return "", fmt.Errorf("no script content provided")
}
