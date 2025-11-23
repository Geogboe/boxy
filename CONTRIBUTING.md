# Contributing to Boxy

Thank you for your interest in contributing to Boxy! This document provides guidelines and instructions for contributing.

## Development Setup

### Release Prerequisites

- Go 1.21 or higher
- Docker (for Docker provider and E2E tests)
- Git
- Task (<https://taskfile.dev>) - install via `sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin`

### Getting Started

1. Clone the repository:

```bash
git clone https://github.com/Geogboe/boxy.git
cd boxy
```

1. Install dependencies:

```bash
go mod download
```

1. Build the project:

```bash
task build
# or
go build ./cmd/boxy
```

## Development Workflow

### Running Tests

We have multiple types of tests:

**Unit Tests** (fast, no external dependencies):

```bash
task test:unit
# or
go test -short ./...
```

**Integration Tests** (use in-memory SQLite, mock providers):

```bash
task test:integration
# or
go test -run Integration ./tests/integration/...
```

**All Tests**:

```bash
task test
# or
go test ./...
```

**With Race Detector**:

```bash
task test:race
```

**Coverage Report**:

```bash
task test:coverage
# Opens coverage.html in browser
```

**Benchmarks**:

```bash
task bench
```

### Code Quality

**Format Code**:

```bash
task fmt
```

**Lint Code**:

```bash
task lint
# Auto-installs golangci-lint if not present
```

**Run All Checks** (format, lint, test, race):

```bash
task check
```

### Building

**Build Binary**:

```bash
task build
# Creates ./boxy executable
```

**Build Release Binary**:

```bash
task build:release
# Creates optimized release binary
```

**Build for All Platforms**:

```bash
task build:all
# Creates binaries in dist/ for all platforms
```

**Install Globally**:

```bash
task install
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
   task check        # Run all quality checks
   task test:all     # Run all test suites
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

```text
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

```text
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

```text
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

## Creating Releases

Boxy uses GitHub Actions to automate the release process. Here's how to create a new release:

### Prerequisites

- Write access to the repository
- All tests passing on main branch
- Changelog/release notes prepared

### Release Process

1. **Ensure main branch is clean and up-to-date**

   ```bash
   git checkout main
   git pull origin main
   ```

2. **Run full test suite**

   ```bash
   task test:all
   task lint
   ```

3. **Create and push a version tag**

   ```bash
   # For a new release (e.g., v1.2.0)
   git tag -a v1.2.0 -m "Release v1.2.0: <brief description>"

   # Push the tag to trigger release workflow
   git push origin v1.2.0
   ```

4. **Monitor the release workflow**
   - Go to <https://github.com/Geogboe/boxy/actions>
   - Watch the "Release" workflow complete
   - It will automatically:
     - Build binaries for all platforms (Linux, macOS, Windows)
     - Generate checksums
     - Create a GitHub release draft
     - Build and push Docker images to GitHub Container Registry

5. **Edit the GitHub release**
   - Go to <https://github.com/Geogboe/boxy/releases>
   - Find the automatically created release
   - Edit the release notes to include:
     - New features
     - Bug fixes
     - Breaking changes (if any)
     - Upgrade instructions (if needed)
   - Publish the release

### Version Naming

Follow [Semantic Versioning](https://semver.org/):

- **MAJOR** (v2.0.0): Breaking changes
- **MINOR** (v1.1.0): New features, backwards compatible
- **PATCH** (v1.0.1): Bug fixes, backwards compatible

### Release Checklist

- [ ] All tests passing (`task test:all`)
- [ ] Linting passing (`task lint`)
- [ ] Documentation updated
- [ ] CHANGELOG updated (if maintained)
- [ ] Version tag follows semver
- [ ] Tag pushed to GitHub
- [ ] Release workflow completed successfully
- [ ] Release notes reviewed and published
- [ ] Docker images verified: `docker pull ghcr.io/geogboe/boxy:<version>`

### Rollback a Release

If you need to rollback a release:

```bash
# Delete the tag locally
git tag -d v1.2.0

# Delete the tag remotely
git push origin :refs/tags/v1.2.0

# Delete the GitHub release (via web UI or gh CLI)
gh release delete v1.2.0
```

## Getting Help

- Check the [README](README.md) for basic usage
- Read [CLAUDE.md](CLAUDE.md) for architectural guidance
- Check [docs/](docs/) for detailed documentation
- Open an issue for questions or problems

## License

By contributing to Boxy, you agree that your contributions will be licensed under the same license as the project.
