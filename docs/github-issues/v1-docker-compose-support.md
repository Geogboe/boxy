# [v1] Docker and Docker Compose Support

**From**: [V1_IMPLEMENTATION_PLAN.md Section 12](../V1_IMPLEMENTATION_PLAN.md#12-docker-and-docker-compose-support)
**Priority**: CRITICAL - Essential for deployment
**Labels**: v1, docker, deployment, documentation

## Overview

Ensure Boxy server runs well in Docker with proper documentation and examples for development and production deployments.

## Implementation Tasks

### Phase 1: Dockerfile
- [ ] Create multi-stage `Dockerfile` for minimal image size
  - Builder stage with Go 1.21
  - Final stage with Alpine Linux
  - Health check command
  - `/data` volume for config/database
- [ ] Test Docker build locally
- [ ] Verify health check works

### Phase 2: Docker Compose - Development
- [ ] Create `examples/docker-compose/dev/docker-compose.yml`
  - Boxy server with Docker socket mount
  - PostgreSQL for multi-tenancy
  - Volume mounts for config and database
- [ ] Create `examples/docker-compose/dev/boxy.yaml` config
- [ ] Create `examples/docker-compose/dev/README.md`
- [ ] Test development example end-to-end

### Phase 3: Docker Compose - Production
- [ ] Create `examples/docker-compose/production/docker-compose.yml`
  - Boxy server with TLS certs
  - PostgreSQL with persistence
  - Prometheus for metrics
  - Grafana for dashboards
  - Proper networking (bridge network)
- [ ] Create production example config files
- [ ] Create `examples/docker-compose/production/README.md`
- [ ] Document security best practices

### Phase 4: Environment Variable Support
- [ ] Implement env var overrides in `internal/config/loader.go`:
  - `BOXY_LOG_LEVEL`
  - `BOXY_SERVER_LISTEN_ADDRESS`
  - `BOXY_STORAGE_PATH`
  - Others as needed
- [ ] Document all supported environment variables

### Phase 5: CI/CD Integration
- [ ] Create `.github/workflows/docker.yml`
  - Build on tag push
  - Multi-platform builds (amd64, arm64)
  - Push to GitHub Container Registry
  - Tag with version and latest
- [ ] Test workflow with dummy tag
- [ ] Document image distribution

### Phase 6: Documentation
- [ ] Create `docs/DOCKER_DEPLOYMENT.md`:
  - Building Docker images
  - Running Boxy in Docker
  - Volume mount guide
  - Environment variables
  - Networking
  - Security best practices
  - Troubleshooting
- [ ] Update README.md with Docker quick start
- [ ] Create `examples/docker-compose/README.md`

### Phase 7: Testing
- [ ] Create `tests/e2e/docker_test.go`:
  - Build Docker image
  - Start with docker-compose
  - Wait for health check
  - Create sandbox via CLI
  - Verify success
  - Cleanup
- [ ] Test development example
- [ ] Test production example
- [ ] Test with various volume mount configurations

## Acceptance Criteria

- [ ] Dockerfile builds successfully
- [ ] Docker image < 50MB (multi-stage build)
- [ ] Health check works correctly
- [ ] Development docker-compose example works
- [ ] Production docker-compose example works
- [ ] Environment variable overrides work
- [ ] Images published to GitHub Container Registry
- [ ] Complete documentation available
- [ ] E2E tests pass with Docker deployment
- [ ] Multi-platform images (amd64, arm64)

## Example Commands

```bash
# Build
docker build -t boxy:v1 .

# Run
docker run -d \
  --name boxy-server \
  -v $(pwd)/boxy.yaml:/data/boxy.yaml \
  -v $(pwd)/boxy.db:/data/boxy.db \
  -p 8443:8443 \
  boxy:v1

# Docker Compose
cd examples/docker-compose/dev
docker-compose up -d

# Pull from registry
docker pull ghcr.io/geogboe/boxy:latest
```

## Files to Create

- `/Dockerfile`
- `/examples/docker-compose/dev/docker-compose.yml`
- `/examples/docker-compose/dev/boxy.yaml`
- `/examples/docker-compose/dev/README.md`
- `/examples/docker-compose/production/docker-compose.yml`
- `/examples/docker-compose/production/boxy.yaml`
- `/examples/docker-compose/production/README.md`
- `/examples/docker-compose/README.md`
- `/docs/DOCKER_DEPLOYMENT.md`
- `/.github/workflows/docker.yml`
- `/tests/e2e/docker_test.go`

## Related Issues

- Depends on: Config location fix (#TBD)
- Related to: Distributed agents (#TBD)
- Blocks: Production deployment (#TBD)

## Notes

- Make sure Docker socket mount works for Docker provider
- Consider security implications of mounting Docker socket
- Document how to run Boxy server in Docker while managing remote Hyper-V agents
