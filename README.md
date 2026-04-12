# Flynn

Flynn is an open source Platform as a Service (PaaS). It is designed to run
anything that can run on Linux, not just stateless web apps. Flynn comes with
highly available database appliances, including PostgreSQL, MySQL, and MongoDB.

Flynn was originally created by Jeff Lindsay, Jonathan Rudenberg, and the team
at Prime Directive, Inc., along with a vibrant open source community. This
repository is a continuation of that work, focused on rebuilding Flynn from its
unmaintained state with modernized infrastructure.

## Status

Flynn is under active reconstruction. The original release infrastructure
(`dl.flynn.io`, `releases.flynn.io`) went offline, and the project's
self-hosting build system depended on it. This fork restores Flynn to a
buildable, testable, and deployable state.

**What has been completed:**

- New TUF (The Update Framework) repository for secure component distribution
- Containerized development environment (no running cluster required to build)
- All 34 component binaries compile from source
- Go version upgraded from 1.13 to 1.22
- CI pipeline via GitHub Actions (build + unit tests)
- End-to-end verified: `flynn-host download` pulls images from the new TUF repo

**What is in progress:**

- Integration testing and cluster bootstrap (discoverd, flannel, controller)
- runc fork modernization (security patches, cgroups v2)


## Repository Layout

This repository (`flynn/`) is a Go monorepo (module `github.com/flynn/flynn`).

| Directory | Description |
|---|---|
| `host/` | `flynn-host` daemon — container management via libcontainer |
| `controller/` | API server, scheduler, worker |
| `cli/` | `flynn` CLI tool |
| `bootstrap/` | Cluster bootstrap actions and manifest |
| `builder/` | Build system, component image definitions |
| `discoverd/` | Service discovery |
| `flannel/` | Container networking (overlay network) |
| `router/` | HTTP/TCP router (L7/L4 load balancer) |
| `appliance/` | Database appliances (PostgreSQL, MariaDB, MongoDB, Redis) |
| `logaggregator/` | Log collection and streaming |
| `blobstore/` | Binary large object storage |
| `pkg/` | Shared Go packages |
| `schema/` | JSON schemas for controller and router APIs |
| `script/` | Build, release, and test orchestration scripts |
| `vendor/` | Vendored Go dependencies |
| `test/` | Integration test runner and test applications |

## Getting Started

### Building from source

Flynn requires a Linux environment with CGO support. The recommended approach
is to use the containerized dev environment:

```sh
# Build core components manually
go build ./host       # flynn-host (requires CGO, Linux)
go build ./cli        # flynn CLI
go build ./controller # flynn-controller

# Build all 34 components at once
make bootstrap-build
```

### Running unit tests

```sh
# Run standalone unit tests (no cluster required)
make test-unit-standalone

# Run specific package tests
go test ./pkg/cors/...
go test ./discoverd/health/...
```

## Contributing

We welcome and encourage community contributions to Flynn.

There are many ways to help:

- Find bugs and file issues
- Improve documentation
- Submit pull requests

Please report issues on [this repository](https://github.com/flynn/flynn/issues)
after searching to see if anyone has already reported the issue.

## Licensing

Flynn is licensed under the BSD 3-Clause license. The original copyright is
held by Prime Directive, Inc. See [LICENSE](./LICENSE) for details.
