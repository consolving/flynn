---
title: Contributing
order: 10
---

Flynn is open source and welcomes contributions.

## Repository

- **Source**: [github.com/consolving/flynn](https://github.com/consolving/flynn)
- **Language**: Go (1.22)
- **Module**: `github.com/flynn/flynn`

## Building

Flynn is a Go monorepo. Build individual components:

```bash
cd flynn/
go build ./host          # flynn-host (requires CGO, Linux)
go build ./cli           # flynn CLI
go build ./controller    # controller API server
```

Most components can be built with `CGO_ENABLED=0` for static binaries. The `host` package requires CGO for libcontainer.

## Project Structure

| Directory | Description |
|-----------|-------------|
| `host/` | flynn-host daemon (container runtime) |
| `controller/` | API server, scheduler |
| `cli/` | Command-line tool |
| `router/` | HTTP/TCP load balancer |
| `discoverd/` | Service discovery |
| `flannel/` | Overlay networking |
| `bootstrap/` | Cluster bootstrap |
| `appliance/` | Database appliances (postgresql, mariadb, mongodb, redis) |
| `pkg/` | Shared packages |
| `vendor/` | Vendored dependencies |

## Testing

```bash
# Unit tests for a specific package
go test ./pkg/cors/...
go test ./discoverd/health/...

# Volume tests require root
sudo go test -race -cover ./host/volume/...
```

Integration tests require a running Flynn cluster. See `script/run-integration-tests` for details.

## Code Style

- Go: `gofmt -s`
- Shell: Google Shell Style Guide
- Commit messages: subsystem prefix required (e.g., `controller: fix scheduler race`)

## Vagrant Development

A Vagrantfile is provided for local development:

```bash
cd vagrant/
vagrant up
```

This provisions a single-node Flynn cluster with Ubuntu Noble 24.04 on KVM/libvirt.

## Key Design Decisions

- **Go monorepo**: All components in one repository for atomic changes
- **Vendored deps**: No network access needed for builds
- **libcontainer**: Direct container management without Docker dependency
- **ZFS**: Copy-on-write storage with snapshot support
- **Squashfs layers**: Immutable, compressed filesystem images for reproducibility
- **TUF**: Cryptographic verification of all distributed artifacts
