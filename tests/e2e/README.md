# End-to-End Tests

This directory contains end-to-end tests that validate the entire Boxy workflow from start to finish.

## Test Files

### `scratch_e2e_test.go`

Tests the complete workflow using the scratch/shell provider, which creates lightweight filesystem-based workspaces.

**Test Coverage:**
- `TestScratchE2E_FullWorkflow`: Full lifecycle test covering:
  - Starting Boxy runtime with scratch provider
  - Pool warming and provisioning
  - Sandbox creation with resource allocation
  - Resource verification and connection info
  - Sandbox listing
  - Sandbox destruction and resource pooling
  - Pool replenishment

- `TestScratchE2E_MultipleResources`: Tests creating sandboxes with multiple resources from the same pool

**What These Tests Validate:**
- ✅ Scratch provider provisions resources correctly
- ✅ Pool manager maintains min_ready resources
- ✅ Sandbox manager allocates resources from pools
- ✅ Resources can be listed and inspected
- ✅ Sandboxes can be destroyed
- ✅ Resources are returned to pools (not destroyed)
- ✅ Pools automatically replenish after allocation

### `docker_e2e_test.go`

Tests with real Docker containers (requires Docker daemon).

**Test Coverage:**
- Pool lifecycle with actual containers
- Container provisioning and health checks
- Multi-container orchestration
- Sandbox management with Docker resources

## Running the Tests

### Run All E2E Tests

```bash
go test -v ./tests/e2e -timeout 10m
```

### Run Only Scratch Tests

```bash
go test -v -run TestScratchE2E ./tests/e2e -timeout 5m
```

### Run Only Docker Tests

```bash
go test -v -run TestDockerE2E ./tests/e2e -timeout 10m
```

### Run in Short Mode (Skips E2E Tests)

```bash
go test -short ./tests/e2e
```

## Test Requirements

### Scratch Tests
- **No external dependencies** - runs entirely with local filesystem
- **Fast** - completes in ~8-10 seconds
- **CI-friendly** - safe to run in any environment

### Docker Tests
- Requires Docker daemon running
- Requires `alpine:latest` and `busybox:latest` images
- Takes longer due to real container operations
- Skipped if Docker is not available

## Test Structure

Each test follows this pattern:

1. **Setup**
   - Create temporary directories
   - Configure pools and providers
   - Initialize storage (in-memory SQLite)
   - Start runtime components

2. **Execute**
   - Wait for pools to reach min_ready
   - Create sandboxes
   - Allocate resources
   - Verify state and connections

3. **Verify**
   - Check resource states
   - Verify filesystem artifacts
   - Test sandbox operations
   - Validate pool behavior

4. **Cleanup**
   - Destroy sandboxes
   - Stop pool managers
   - Stop runtime
   - Clean up temporary files

## Known Behaviors

### Resource Lifecycle

**Important:** When sandboxes are destroyed, resources are **returned to the pool**, not destroyed. This is by design for efficiency:

```go
// After destroying a sandbox:
// ✅ Resources return to "ready" state in pool
// ✅ Resource directories still exist
// ✅ Pool maintains min_ready resources
// ❌ Resources are NOT destroyed/deleted
```

### Resource ID vs ProviderID

Resources have two IDs:
- `resource.ID`: Internal UUID used by Boxy
- `resource.ProviderID`: Provider-specific identifier (for scratch provider, this is the full directory path)

**Always use `ProviderID` for filesystem operations** with the scratch provider.

## Adding New Tests

When adding new E2E tests:

1. **Follow the naming convention**: `Test<Provider>E2E_<Scenario>`
2. **Use the scratch provider for fast tests**: No dependencies, quick execution
3. **Add Docker tests for integration validation**: Real isolation, production-like
4. **Document what you're testing**: Add comments explaining the test purpose
5. **Use proper cleanup**: Defer cleanup functions in correct order
6. **Make tests independent**: Don't rely on execution order

### Example Test Template

```go
func TestScratchE2E_MyScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	ctx := context.Background()
	testDir := t.TempDir() // Auto-cleaned up

	// Setup
	cfg := &config.Config{...}
	store, err := storage.NewSQLiteStore(filepath.Join(testDir, "test.db"))
	require.NoError(t, err)
	defer store.Close()

	// ... setup providers and runtime ...

	defer func() {
		// Cleanup in correct order
		for _, pm := range rt.Pools {
			pm.Stop()
		}
		rt.Stop(logger)
	}()

	// Test logic
	t.Log("Step 1: ...")
	// ... test steps ...

	t.Log("Test PASSED")
}
```

## Debugging Tests

### Enable Verbose Logging

Set the `TEST_VERBOSE` environment variable:

```bash
TEST_VERBOSE=1 go test -v ./tests/e2e
```

### Check Temporary Directories

Tests use `t.TempDir()` which auto-cleans up. To inspect:

```bash
# Run without cleanup
go test -v -run TestScratchE2E_FullWorkflow ./tests/e2e

# Find temp directories while test is running
find /tmp -name "boxy-e2e-*" -type d
```

### Run Single Test

```bash
go test -v -run "TestScratchE2E_FullWorkflow$" ./tests/e2e
```

## CI Integration

These tests are designed for CI/CD pipelines:

```yaml
# Example GitHub Actions
- name: Run E2E Tests
  run: |
    go test -v ./tests/e2e -timeout 10m

- name: Run Scratch Tests Only (Fast)
  run: |
    go test -v -run TestScratchE2E ./tests/e2e -timeout 5m
```

## Troubleshooting

### Test Timeout

If tests timeout:
- Check if Docker daemon is slow to start containers
- Increase timeout: `-timeout 15m`
- Run with verbose logging to see where it hangs

### Permission Errors

Scratch tests create directories in `/tmp`. Ensure write permissions:

```bash
ls -la /tmp/boxy-e2e-*
```

### Resource ID Mismatch

If you see resource ID != ProviderID path mismatch:
- This is expected due to how resources are stored/retrieved
- Always use `resource.ProviderID` for the actual filesystem path
- This is a known limitation documented in the test comments

## Future Improvements

Potential enhancements for these tests:

- [ ] Add CLI integration tests (currently commented out)
- [ ] Add API endpoint tests when API server is implemented
- [ ] Add distributed agent tests when remote agents are ready
- [ ] Add hook execution tests
- [ ] Add failure scenario tests (network issues, disk full, etc.)
- [ ] Add performance/stress tests
- [ ] Add tests for AllocateArtifacts integration
