# 10: Debugging Guide

---

## Metadata

```yaml
feature: "Debugging Documentation"
slug: "debugging-guide"
status: "not-started"
priority: "medium"
type: "documentation"
effort: "small"
depends_on: []
enables: ["troubleshooting", "support"]
testing: ["manual"]
breaking_change: false
week: "7-8"
related_docs:
  - "testing-strategy.md"
```

---

## Overview

Comprehensive troubleshooting guide covering:

- Log levels and interpretation
- Common issues and solutions
- Diagnostic commands
- Debugging tools (Delve, pprof)
- Network debugging (agents)

---

## Log Levels

```bash
# Enable debug logging
BOXY_LOG_LEVEL=debug boxy serve

# Structured JSON logs
BOXY_LOG_FORMAT=json boxy serve
```

---

## Common Issues

### "Pool has no ready resources"

- **Cause**: Preheating disabled or provisioning slow
- **Solution**: Enable preheating or check provider health

### "Agent connection refused"

- **Cause**: Agent not running or certificate mismatch
- **Solution**: Check agent status, verify mTLS certs

### "Quota exceeded"

- **Cause**: User hit sandbox limit
- **Solution**: Destroy old sandboxes or increase quota

---

## Diagnostic Commands

```bash
# Check pool health
boxy pool stats <pool-name>

# Check resource states
boxy pool resources <pool-name>

# Verify agent connection
boxy admin agent status <agent-id>

# Test provider
boxy admin test-provider --backend hyperv
```

---

## Success Criteria

- ✅ Log level guide written
- ✅ Common issues documented
- ✅ Diagnostic commands documented
- ✅ Debugging tools documented
- ✅ FAQ section created

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: None
**Blocking**: None
