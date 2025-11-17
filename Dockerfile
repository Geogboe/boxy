# Multi-stage build for minimal final image
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags='-s -w -extldflags "-static"' \
    -o boxy ./cmd/boxy

# Final stage - minimal runtime image
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 boxy && \
    adduser -D -u 1000 -G boxy boxy

WORKDIR /home/boxy

# Copy binary from builder
COPY --from=builder /build/boxy /usr/local/bin/boxy

# Copy example config
COPY boxy.example.yaml /home/boxy/boxy.example.yaml

# Create directories for data
RUN mkdir -p /home/boxy/data /home/boxy/.config/boxy && \
    chown -R boxy:boxy /home/boxy

# Switch to non-root user
USER boxy

# Expose default ports (if any)
# EXPOSE 8080

# Set default command
ENTRYPOINT ["/usr/local/bin/boxy"]
CMD ["serve"]

# Labels
LABEL org.opencontainers.image.title="Boxy" \
      org.opencontainers.image.description="Sandboxing orchestration tool for mixed virtual environments" \
      org.opencontainers.image.source="https://github.com/Geogboe/boxy" \
      org.opencontainers.image.licenses="MIT"
