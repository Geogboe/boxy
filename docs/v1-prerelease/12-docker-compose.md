# 12: Docker & Docker Compose Support

---

## Metadata

```yaml
feature: "Docker & Compose Support"
slug: "docker-compose"
status: "not-started"
priority: "high"
type: "feature"
effort: "medium"
depends_on: ["config-location"]
enables: ["easy-deployment", "testing"]
testing: ["integration", "e2e"]
breaking_change: false
week: "7-8"
related_docs:
  - "11-config-location.md"
  - "07-distributed-agents.md"
```

---

## Overview

Support running Boxy in Docker containers:
- Dockerfile for Boxy server
- Dockerfile for Boxy agent  
- Docker Compose examples
- Volume mounts for config/data
- Container-to-container networking

---

## Dockerfile

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o boxy ./cmd/boxy

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/boxy /usr/local/bin/boxy
WORKDIR /app
CMD ["boxy", "serve"]
```

---

## Docker Compose Examples

### Example 1: Boxy Server + Docker Provider

```yaml
version: '3.8'
services:
  boxy-server:
    image: ghcr.io/geogboe/boxy:v1
    volumes:
      - ./boxy.yaml:/app/boxy.yaml:ro
      - ./data:/app/data
      - /var/run/docker.sock:/var/run/docker.sock  # Docker provider
    ports:
      - "8443:8443"
```

### Example 2: Boxy Server + Remote Agent

```yaml
version: '3.8'
services:
  boxy-server:
    image: ghcr.io/geogboe/boxy:v1
    volumes:
      - ./boxy.yaml:/app/boxy.yaml:ro
      - ./data:/app/data
      - ./certs:/app/certs:ro
    ports:
      - "8443:8443"

  # Agent would run on Windows host separately
```

---

## Success Criteria

- ✅ Dockerfile builds successfully
- ✅ Docker Compose examples work
- ✅ Config/data mounted correctly
- ✅ Docker provider works in container
- ✅ Agent can connect from container
- ✅ Documentation complete

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: 11-config-location
**Blocking**: None
