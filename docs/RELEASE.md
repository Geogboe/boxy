# Release Process

This document describes how to create a new release of Boxy.

## Overview

Boxy uses automated release pipelines triggered by Git tags. When you push a version tag (e.g., `v1.0.0`), GitHub Actions automatically:

1. Creates a GitHub release with changelog
2. Builds binaries for all platforms (Linux, macOS, Windows for amd64/arm64)
3. Uploads compressed binaries with checksums
4. Builds and pushes Docker images to GitHub Container Registry

## Prerequisites

- Maintainer access to the repository
- All tests passing on main branch
- Updated documentation

## Release Steps

### 1. Prepare the Release

**Update VERSION** (if you have a VERSION file):

```bash
echo "1.0.0" > VERSION
git add VERSION
git commit -m "chore: bump version to 1.0.0"
```

**Update CHANGELOG** (if maintaining one manually):

```bash
# Edit CHANGELOG.md with new version section
git add CHANGELOG.md
git commit -m "docs: update CHANGELOG for v1.0.0"
```

**Ensure all changes are merged**:

```bash
git checkout main
git pull origin main
```

### 2. Create and Push Tag

**Create an annotated tag**:

```bash
# Format: v<MAJOR>.<MINOR>.<PATCH>
git tag -a v1.0.0 -m "Release v1.0.0"
```

**Push the tag to trigger release**:

```bash
git push origin v1.0.0
```

This automatically triggers the GitHub Actions release workflow.

### 3. Monitor the Release

1. Go to **Actions** tab in GitHub
2. Watch the "Release" workflow execute
3. Verify all build jobs complete successfully

### 4. Verify the Release

Once the workflow completes:

1. Go to **Releases** tab
2. Verify the new release appears with:
   - Correct version number
   - Generated changelog
   - Binary assets for all platforms
   - SHA256 checksums for each binary
   - Docker image tags

**Download and test a binary**:

```bash
# Example for Linux amd64
wget https://github.com/Geogboe/boxy/releases/download/v1.0.0/boxy-linux-amd64.tar.gz
tar -xzf boxy-linux-amd64.tar.gz
./boxy-linux-amd64 version
```

**Test the Docker image**:

```bash
docker pull ghcr.io/geogboe/boxy:1.0.0
docker run --rm ghcr.io/geogboe/boxy:1.0.0 version
```

### 5. Post-Release

**Announce the release**:

- Update README if needed
- Post on social media/forums
- Notify users via appropriate channels

**Start next development cycle**:

```bash
# Optionally create a new development tag
git tag -a v1.1.0-dev -m "Start v1.1.0 development"
git push origin v1.1.0-dev
```

## Versioning Strategy

Boxy follows [Semantic Versioning](https://semver.org/):

- **MAJOR** version: Incompatible API changes
- **MINOR** version: New functionality (backward compatible)
- **PATCH** version: Bug fixes (backward compatible)

### Examples

- `v1.0.0` - First stable release
- `v1.1.0` - New feature added
- `v1.1.1` - Bug fix
- `v2.0.0` - Breaking changes
- `v1.0.0-beta.1` - Pre-release (marked as prerelease)
- `v1.0.0-rc.1` - Release candidate

## Pre-releases

For alpha, beta, or release candidate versions:

```bash
git tag -a v1.0.0-beta.1 -m "Release v1.0.0-beta.1"
git push origin v1.0.0-beta.1
```

The release workflow automatically marks releases as "prerelease" if the tag contains:

- `alpha`
- `beta`
- `rc`

## Release Artifacts

Each release includes:

### Binaries

- `boxy-linux-amd64.tar.gz` + `.sha256`
- `boxy-linux-arm64.tar.gz` + `.sha256`
- `boxy-darwin-amd64.tar.gz` + `.sha256`
- `boxy-darwin-arm64.tar.gz` + `.sha256`
- `boxy-windows-amd64.exe.zip` + `.sha256`

### Docker Images

Pushed to GitHub Container Registry with tags:

- `ghcr.io/geogboe/boxy:latest`
- `ghcr.io/geogboe/boxy:1.0.0`
- `ghcr.io/geogboe/boxy:1.0`
- `ghcr.io/geogboe/boxy:1`

## Troubleshooting

### Release workflow failed

1. Check the Actions logs for specific errors
2. Fix the issue in code
3. Delete the tag locally and remotely:

   ```bash
   git tag -d v1.0.0
   git push origin :refs/tags/v1.0.0
   ```

4. Create and push the tag again

### Binary is missing or corrupt

- Verify the build step in Actions logs
- Check the Go version and build flags
- Ensure cross-compilation is working

### Docker image not pushed

- Verify `GITHUB_TOKEN` has correct permissions
- Check Docker login step in Actions logs
- Ensure Dockerfile builds successfully

### Changelog is wrong

The changelog is auto-generated from commit messages. To improve:

- Use conventional commit format
- Write descriptive commit messages
- Manually edit release notes on GitHub after creation

## Manual Release (Emergency)

If automation fails, you can create a release manually:

1. Build binaries:

   ```bash
   make build-all  # If you have a make target
   # Or manually:
   GOOS=linux GOARCH=amd64 go build -o boxy-linux-amd64 ./cmd/boxy
   ```

2. Create GitHub release via web UI
3. Upload artifacts manually
4. Build and push Docker image:

   ```bash
   docker build -t ghcr.io/geogboe/boxy:1.0.0 .
   docker push ghcr.io/geogboe/boxy:1.0.0
   ```

## Security

### Signing Releases

To add GPG signing to releases (optional):

1. Generate GPG key
2. Add to GitHub settings
3. Sign tags:

   ```bash
   git tag -s v1.0.0 -m "Release v1.0.0"
   ```

### Checksum Verification

Users can verify downloads:

```bash
# Download binary and checksum
wget https://github.com/Geogboe/boxy/releases/download/v1.0.0/boxy-linux-amd64.tar.gz
wget https://github.com/Geogboe/boxy/releases/download/v1.0.0/boxy-linux-amd64.tar.gz.sha256

# Verify
sha256sum -c boxy-linux-amd64.tar.gz.sha256
```

## Best Practices

1. **Test before release**: Run full test suite
2. **Review changes**: Check git log since last release
3. **Update docs**: Ensure documentation reflects new features
4. **Coordinate timing**: Release during business hours
5. **Monitor**: Watch for issues after release
6. **Communicate**: Announce releases to users

## Resources

- [Semantic Versioning](https://semver.org/)
- [Conventional Commits](https://www.conventionalcommits.org/)
- [GitHub Releases](https://docs.github.com/en/repositories/releasing-projects-on-github)
- [Docker Build Push Action](https://github.com/docker/build-push-action)
