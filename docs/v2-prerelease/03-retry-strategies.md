# 03: Retry Strategies

---

## Metadata

```yaml
feature: "Retry Strategies"
slug: "retry-strategies"
status: "not-started"
priority: "medium"
type: "feature"
effort: "medium"
depends_on: []
enables: ["fault-tolerance", "reliability"]
testing: ["unit", "integration"]
breaking_change: false
week: "4-6"
```

---

## Overview

Automatic retry with exponential backoff for transient failures:

- Provider operations (Provision, Destroy, Update)
- Hook execution
- Agent communication
- Database operations

**Goal**: Handle transient failures gracefully without manual intervention.

---

## Configuration

```yaml
pools:
  - name: win-test-vms
    retry:
      enabled: true
      max_attempts: 3
      backoff: exponential  # 1s, 2s, 4s, 8s...
      operations:
        - provision
        - warmup
        - hooks
```

---

## Implementation

```go
type RetryConfig struct {
    Enabled     bool
    MaxAttempts int
    Backoff     string  // "exponential" or "linear"
}

func RetryWithBackoff(ctx context.Context, config RetryConfig, fn func() error) error {
    for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
        err := fn()
        if err == nil {
            return nil // Success!
        }

        if attempt == config.MaxAttempts {
            return fmt.Errorf("max retries exceeded: %w", err)
        }

        // Calculate backoff
        wait := calculateBackoff(attempt, config.Backoff)
        time.Sleep(wait)
    }
    return nil
}
```

---

## Success Criteria

- ✅ Retry logic implemented
- ✅ Exponential backoff works
- ✅ Max attempts respected
- ✅ Logs show retry attempts
- ✅ Integration tests pass

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: None
