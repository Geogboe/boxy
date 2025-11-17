# Contributing to Boxy

Thank you for your interest in contributing to Boxy! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.21 or higher
- Docker (for Docker provider and E2E tests)
- Git

### Getting Started

1. Clone the repository:
```bash
git clone https://github.com/Geogboe/boxy.git
cd boxy
```

2. Install dependencies:
```bash
go mod download
```

3. Build the project:
```bash
make build
# or
go build ./cmd/boxy
```

## Development Workflow

### Running Tests

We have multiple types of tests:

**Unit Tests** (fast, no external dependencies):
```bash
make test-unit
# or
go test -short ./...
```

**Integration Tests** (use in-memory SQLite, mock providers):
```bash
make test-integration
# or
go test -run Integration ./tests/integration/...
```

**All Tests**:
```bash
make test
# or
go test ./...
```

**With Race Detector**:
```bash
make test-race
```

**Coverage Report**:
```bash
make test-coverage
# Opens coverage.html in browser
```

**Benchmarks**:
```bash
make bench
```

### Code Quality

**Format Code**:
```bash
make fmt
```

**Lint Code**:
```bash
make lint
# Requires golangci-lint: https://golangci-lint.run/usage/install/
```

**Run All Checks** (format, lint, test, race):
```bash
make check
```

### Building

**Build Binary**:
```bash
make build
# Creates ./boxy executable
```

**Install Globally**:
```bash
make install
# Installs to /usr/local/bin/boxy
```

## Pull Request Process

1. **Fork and Branch**
   - Fork the repository
   - Create a feature branch: `git checkout -b feature/my-feature`

2. **Make Changes**
   - Write clear, concise commit messages following [Conventional Commits](https://www.conventionalcommits.org/)
   - Add tests for new functionality
   - Update documentation as needed

3. **Test Locally**
   ```bash
   make check        # Run all quality checks
   make test-all     # Run all test suites
   ```

4. **Commit and Push**
   ```bash
   git add .
   git commit -m "feat: add new feature"
   git push origin feature/my-feature
   ```

5. **Open Pull Request**
   - Provide clear description of changes
   - Reference any related issues
   - Ensure CI passes

## Commit Message Guidelines

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Types

- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Adding or updating tests
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `chore`: Maintenance tasks
- `ci`: CI/CD changes

### Examples

```
feat(pool): add support for hybrid warm/cold pools
fix(docker): resolve container cleanup race condition
docs(readme): update installation instructions
test(sandbox): add integration tests for multi-pool allocation
```

## Code Style

- Follow standard Go conventions
- Use `gofmt` and `goimports`
- Pass `golangci-lint` checks
- Write clear comments for exported functions
- Keep functions focused and testable

## Testing Guidelines

### Unit Tests

- Test individual functions and methods
- Mock external dependencies
- Fast execution (< 1 second per test)
- Use table-driven tests for multiple cases

Example:
```go
func TestPoolConfig_Validate(t *testing.T) {
    tests := []struct {
        name    string
        config  *PoolConfig
        wantErr bool
    }{
        {
            name:    "valid config",
            config:  &PoolConfig{Name: "test", MinReady: 3, MaxTotal: 10},
            wantErr: false,
        },
        // ... more cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.config.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("wanted error: %v, got: %v", tt.wantErr, err)
            }
        })
    }
}
```

### Integration Tests

- Test component interactions
- Use real storage (in-memory SQLite)
- Use mock providers (no Docker required)
- Mark with `testing.Short()` skip
- Use helper functions from `tests/integration/helpers.go`

Example:
```go
func TestPoolManager_Integration_Allocation(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)
    // ... test code
}
```

## Project Structure

```
boxy/
├── cmd/boxy/              # CLI application
├── internal/
│   ├── core/              # Domain logic
│   │   ├── pool/          # Pool management
│   │   ├── resource/      # Resource abstractions
│   │   └── sandbox/       # Sandbox orchestration
│   ├── provider/          # Provider implementations
│   │   ├── docker/        # Docker backend
│   │   └── mock/          # Mock provider for testing
│   ├── storage/           # Persistence layer
│   └── config/            # Configuration management
├── pkg/                   # Public packages
│   └── provider/          # Provider interface
├── tests/
│   └── integration/       # Integration tests
├── docs/                  # Documentation
└── .github/workflows/     # CI/CD pipelines
```

## Getting Help

- Check the [README](README.md) for basic usage
- Read [CLAUDE.md](CLAUDE.md) for architectural guidance
- Check [docs/](docs/) for detailed documentation
- Open an issue for questions or problems

## License

By contributing to Boxy, you agree that your contributions will be licensed under the same license as the project.
