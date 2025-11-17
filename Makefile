.PHONY: help build build-release build-all test test-unit test-integration test-e2e test-stress test-all test-race test-coverage bench clean install

# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildDate=$(BUILD_DATE)

# Default target
help:
	@echo "Boxy - Makefile targets:"
	@echo ""
	@echo "  make build            - Build the boxy binary (with version info)"
	@echo "  make build-release    - Build optimized release binary"
	@echo "  make build-all        - Build for all platforms"
	@echo "  make install          - Install boxy to /usr/local/bin"
	@echo "  make test             - Run all tests"
	@echo "  make test-unit        - Run unit tests only"
	@echo "  make test-integration - Run integration tests (requires Docker)"
	@echo "  make test-e2e         - Run end-to-end tests (requires Docker)"
	@echo "  make test-stress      - Run stress tests"
	@echo "  make test-race        - Run tests with race detector"
	@echo "  make test-coverage    - Generate coverage report"
	@echo "  make bench            - Run benchmarks"
	@echo "  make clean            - Clean build artifacts"
	@echo ""

# Build the binary with version information
build:
	@echo "Building boxy $(VERSION)..."
	go build -ldflags="$(LDFLAGS)" -o boxy ./cmd/boxy
	@echo "✓ Build complete: ./boxy"
	@./boxy version

# Build optimized release binary
build-release:
	@echo "Building release binary $(VERSION)..."
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o boxy ./cmd/boxy
	@echo "✓ Release build complete: ./boxy"

# Build for all platforms
build-all:
	@echo "Building for all platforms..."
	@mkdir -p dist
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o dist/boxy-linux-amd64 ./cmd/boxy
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o dist/boxy-linux-arm64 ./cmd/boxy
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o dist/boxy-darwin-amd64 ./cmd/boxy
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o dist/boxy-darwin-arm64 ./cmd/boxy
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o dist/boxy-windows-amd64.exe ./cmd/boxy
	@echo "✓ All builds complete in dist/"
	@ls -lh dist/

# Install to system
install: build
	@echo "Installing boxy to /usr/local/bin..."
	sudo mv boxy /usr/local/bin/
	@echo "✓ Installed"

# Run all tests
test: test-unit
	@echo "✓ All tests passed"

# Run unit tests only (fast)
test-unit:
	@echo "Running unit tests..."
	go test -v -short ./...

# Run integration tests (requires Docker)
test-integration:
	@echo "Running integration tests..."
	go test -v -run Integration ./tests/integration/...

# Run end-to-end tests
test-e2e:
	@echo "Running E2E tests..."
	@echo "Building binary for E2E tests..."
	go build -o boxy ./cmd/boxy
	go test -v -timeout 10m ./tests/e2e/...

# Run stress tests
test-stress:
	@echo "Running stress tests..."
	go test -v -timeout 30m ./tests/stress/...

# Run all test suites
test-all: test-unit test-integration test-e2e
	@echo "✓ All test suites passed!"

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	go test -race -short ./...

# Generate coverage report
test-coverage:
	@echo "Generating coverage report..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"

# Run benchmarks
bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f boxy
	rm -rf dist/
	rm -f coverage.out coverage.html
	rm -f *.db *.db-shm *.db-wal
	rm -rf tests/tmp
	@echo "✓ Clean complete"

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "✓ Format complete"

# Lint code
lint:
	@echo "Linting code..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...
	@echo "✓ Lint complete"

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	go mod tidy
	@echo "✓ Tidy complete"

# Development mode - build and run
dev: build
	./boxy serve

# Quick check - format, lint, and test
check: fmt lint test-unit test-race
	@echo "✓ All checks passed!"
