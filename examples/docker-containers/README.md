# docker-containers

This example provisions real Docker containers and requires a running Docker
daemon. Start it with `./serve.sh` on Linux/macOS or `./serve.ps1` on Windows;
the image and daemon prerequisites are documented in `boxy.yaml`.

## Try a built-in package recipe

The `client` pool uses the `alpine` image, so it is a useful place to try the
`apk` recipe. Add this package and reference it from that pool:

```yaml
packages:
  client-tools:
    builtin: package-manager
    version: 1.0.0
    scopes: [resource]
    events: [provision]
    inputs:
      parameters:
        manager: apk
        packages: [curl, git]
```

```yaml
    packages: [client-tools@1.0.0]
```

The container must have network access to its configured Alpine repositories,
the `apk` executable must already exist, and the container user must have
permission to install packages. The example's default Alpine container runs as
root. Versions follow the repository; the recipe does not pin them. If `apk`
is missing or returns a nonzero exit code, provisioning fails and the package
is not recorded as applied.

For an Ubuntu-based pool, use the same declaration with `manager: apt`.
`apt-get update` and the noninteractive install then require reachable Debian
repositories and root or equivalent package-install privilege.

See [`docs/package-artifacts.md`](../../docs/package-artifacts.md) for the
complete recipe contract. This example does not bootstrap a package manager.
