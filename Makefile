.PHONY: help build test test-unit test-integration test-e2e test-stress test-all test-race test-coverage bench clean install

# Default target
help:
	@echo "Boxy - Makefile targets:"
	@echo ""
	@echo "  make build            - Build the boxy binary"
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

# Build the binary
build:
	@echo "Building boxy..."
	go build -o boxy ./cmd/boxy
	@echo "✓ Build complete: ./boxy"

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
