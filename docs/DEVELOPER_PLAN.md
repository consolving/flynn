# Flynn Developer Plan

This plan documents the build process and dependencies for the Flynn PaaS project.

## Project Overview

**Project**: Flynn (unmaintained PaaS platform)
**Location**: `/home/philipp/GIT/flynn`
**Primary Language**: Go (1.13)
**Build System**: Tup + Make + Docker

**Important Notice**: Flynn is unmaintained and all public infrastructure has been shut down. This plan is for development/research purposes only.

---

## Prerequisites

### System Requirements

1. **Operating System**: Ubuntu 16.04 or 18.04 (64-bit)
   - Container runtime: Docker 1.9.1
   - Build system: Tup
   - Go toolchain: Go 1.13+

2. **Essential Tools**
   ```
   - Docker 1.9.1 (specific version required)
   - Tup (directory-based build system)
   - Go 1.13+ (go, gofmt)
   - jq (JSON processing)
   - curl, sha512sum
   - pkg-config, libseccomp-dev
   ```

3. **Databases** (for testing)
   - PostgreSQL
   - MariaDB/Percona
   - MongoDB
   - Redis

### Optional: Vagrant Development VM

For easier setup, use the provided Vagrantfile:
```bash
vagrant up
vagrant ssh
```

---

## Build Architecture

### Key Build Components

```
flynn/
├── build/              # Build output directory
│   ├── bin/           # Compiled binaries
│   ├── image/         # Docker images (JSON manifests)
│   ├── manifests/     # Deployment manifests
│   └── _go/           # Go toolchain (GOROOT)
├── script/            # Build and deployment scripts
│   ├── build-flynn    # Main build orchestration
│   ├── flynn-builder  # Builder image wrapper
│   ├── bootstrap-flynn # Cluster bootstrap
│   └── ...
├── Tupfile.ini        # Tup configuration
├── tup.config         # Tup build rules
├── Makefile           # High-level build targets
├── builder/           # Builder Docker images
├── vendor/            # Go vendored dependencies
├── go.mod             # Go module definition
└── docs/              # Documentation
```

### Build Flow

```
User runs: make
    ↓
script/build-flynn executes
    ↓
1. Downloads flynn-host binary (if not present)
2. Downloads Flynn release binaries/images via TUF
3. Extracts binaries and Docker image manifests
4. Boots/bootstrap Flynn cluster
5. Runs build inside Docker container:
   - flynn-builder script
   - Builds Go components
   - Creates Docker images
    ↓
Binaries extracted to build/bin/
Images stored in build/image/
```

### Build Scripts

#### `script/build-flynn`
- Downloads pre-built binaries from TUF repository
- Bootstraps Flynn cluster if not running
- Builds Flynn inside Docker container
- Extracts binaries and Go toolchain to `build/`

**Options:**
- `-v, --verbose`: Verbose output
- `--host=HOST`: Host to run build on
- `--git-version`: Generate version from git
- `-f, --force-bootstrap`: Force cluster bootstrap

#### `script/flynn-builder`
- Wrapper for build environment
- Sets `GO111MODULE=on` and `GOFLAGS=-mod=vendor`
- Calls actual `flynn-builder` binary

#### `script/bootstrap-flynn`
- Starts `flynn-host` daemon
- Runs Flynn services
- Options for multi-node clusters

---

## Build Dependencies

### Go Module Dependencies (from `go.mod`)

Key dependencies include:
- `github.com/flynn/go-tuf` - Update framework for secure distribution
- `github.com/docker/go-units` - Docker utilities
- `github.com/golang/protobuf` - Protocol buffers
- `google.golang.org/grpc` - gRPC framework
- Various Azure/AWS/GCP SDKs for cloud integration
- Database drivers (PostgreSQL, MySQL, MongoDB, Redis)

**Replace directives:**
- `github.com/opencontainers/runc` → `github.com/flynn/runc`
- `github.com/godbus/dbus` → `github.com/godbus/dbus/v5`
- `github.com/coreos/pkg` → `github.com/flynn/coreos-pkg`

### TUF Repository Configuration

**Current config** (`tup.config`):
```
CONFIG_IMAGE_REPOSITORY=https://dl.flynn.io/tuf
CONFIG_TUF_ROOT_KEYS=[...]
```

**Note**: The TUF repository (`dl.flynn.io`) has been shut down. This must be addressed for building.

---

## Building Flynn

### Step-by-Step Build Process

#### Method 1: Using Make (Recommended)

```bash
cd /home/philipp/GIT/flynn

# Clean previous build (optional)
make clean

# Build Flynn
make

# Build with git-derived version
make release
```

#### Method 2: Direct Script Execution

```bash
# Build with default version
./script/build-flynn

# Build with specific version
./script/build-flynn --version custom-version

# Build with verbose output
./script/build-flynn -v --git-version
```

### What Gets Built

After a successful build, the following are created:

**Binaries** (`build/bin/`):
- `flynn-host` - Container runtime daemon
- `flynn-init` - Cluster init tool
- `flynn` - CLI tool (symlink to flynn-linux-amd64)
- `flynn-linux-amd64`, `flynn-linux-386` - CLI targets
- `flynn-darwin-amd64`, `flynn-windows-amd64` - CLI targets
- `flynn-builder` - Builder image runner
- `discoverd` - Service discovery
- `flynn-release` - Release manager
- `tuf`, `tuf-client` - TUF tools

**Go toolchain** (`build/_go/`):
- Symlink to Go installation inside builder image

**Docker image manifests** (`build/image/`):
- `builder.json`, `host.json`, `go.json`, etc.
- Contains image IDs and metadata

**Manifests** (`build/manifests/`):
- Deployment configuration files

---

## Running Flynn

### Bootstrap Cluster

```bash
# Boot a single-node cluster
./script/bootstrap-flynn

# Boot multi-node cluster (3 nodes)
./script/bootstrap-flynn --size 3

# Boot from specific version
./script/bootstrap-flynn --version v20151104.1
```

### Check Running Services

```bash
# List running jobs
flynn-host ps

# View all jobs (including stopped)
flynn-host ps -a

# View job logs
flynn-host log <JOBID>
```

---

## Testing

### Unit Tests

```bash
make test-unit           # All unit tests with race detection
make test-unit-root      # Unit tests requiring root (volume tests)
```

Or directly:
```bash
go test -race -cover ./...
go test ./router          # Specific component
go test ./controller/...  # Component + subpackages
```

### Integration Tests

```bash
make test-integration              # All integration tests
script/run-integration-tests       # With script wrapper
script/run-integration-tests -f TestEnvDir  # Specific test
```

---

## Build Troubleshooting

### Common Issues

1. **TUF Repository Unreachable**
   - Error: Cannot download from `dl.flynn.io`
   - Solution: You'll need to host TUF repository locally or set up your own

2. **Docker Version Too New**
   - Flynn requires Docker 1.9.1 (very old)
   - Solution: Install older Docker version or use Vagrant VM

3. **Build Failed Mid-Process**
   - Run `make clean` and rebuild
   - Check Docker daemon is running

4. **Permission Issues**
   - Many build steps require sudo
   - Ensure user is in docker group

### Debug Commands

```bash
# Check flynn-host daemon log
less /tmp/flynn-host.log

# Collect debug information
flynn-host collect-debug-info

# Stop all Flynn services
./script/kill-flynn

# Re-run bootstrap with specific steps
./script/bootstrap-flynn --steps discoverd,flannel,wait-hosts
```

---

## Project Architecture

### Major Components

| Component | Description |
|-----------|-------------|
| `host` | `flynn-host` daemon for running containers |
| `controller` | HTTP API for managing applications |
| `router` | Reverse proxy for routing traffic |
| `blobstore` | Object storage (S3/GCS/Azure compatible) |
| `discoverd` | Service discovery |
| `slugbuilder/slugrunner` | Buildpack-based app build system |
| `gitreceive` | Git push handler |
| `logaggregator` | Centralized logging |
| `cli` | Command-line interface |

### Container Images

**Builder images** (`generator/builder`):
- Base OS images: ubuntu-trusty, ubuntu-xenial, ubuntu-bionic
- Toolchain: go, node, ruby, python, php, java
- Build tools: builder, build-tools (TUF)
- Appliance images: postgres, mysql, redis, mongodb

---

## Versioning

### Version Format

- **Git-derived**: `<branch>-<commit>` (e.g., `master-a1b2c3d`)
- **Tags**: `<tag>-<commit>` (e.g., `v20171206.lmars-abc123`)
- **Explicit**: User-defined via `--version` flag

---

## Legacy Infrastructure Notice

**Critical Discontinuations:**
- TUF repository (`dl.flynn.io`) - **SHUT DOWN**
- Binary releases ('releases.flynn.io') - **SHUT DOWN**
- Documentation hosting - No active maintenance

**Recommendations:**
- For production: Use Kubernetes, Docker Swarm, or modern PaaS
- For development/research: Set up local TUF repo or use old code snapshots
- Consider migrating to: Cloud Foundry Diego, Herokuish, or Kubernetes-based solutions

---

## References

- **Development Guide**: `docs/content/development.html.md`
- **Contribution Guide**: `CONTRIBUTING.md`
- **Test Documentation**: `test/README.md`
- **Go Documentation**: `go.doc` (in code)
- **Project Homepage**: https://flynn.io (archived)

---

**Generated**: April 11, 2026
**Document Version**: 1.0
