---
title: Architecture
order: 8
---

Flynn is a modular PaaS composed of several interacting services. All components are written in Go and communicate via JSON/HTTP APIs.

## System Overview

```
┌─────────────────────────────────────────────────────────┐
│                       flynn-host                         │
│  (container runtime, process management, host API)      │
├─────────────────────────────────────────────────────────┤
│  discoverd     │  flannel      │  router                │
│  (service      │  (overlay     │  (HTTP/TCP             │
│   discovery)   │   network)    │   load balancer)       │
├────────────────┼───────────────┼────────────────────────┤
│  controller    │  scheduler    │  blobstore             │
│  (API server)  │  (job         │  (artifact             │
│                │   placement)  │   storage)             │
├────────────────┼───────────────┼────────────────────────┤
│  gitreceive    │  slugbuilder  │  slugrunner            │
│  (git push     │  (buildpack   │  (app runtime          │
│   endpoint)    │   execution)  │   container)           │
├────────────────┴───────────────┴────────────────────────┤
│  Database Appliances: PostgreSQL, MariaDB, MongoDB, Redis│
└─────────────────────────────────────────────────────────┘
```

## flynn-host

The foundation of Flynn. Runs on every node as a systemd service. Responsibilities:

- **Container management**: Creates and manages containers using libcontainer (Linux namespaces, cgroups)
- **Image management**: Downloads and verifies component images via TUF
- **Job scheduling**: Receives job requests from the scheduler and starts containers
- **Host API**: Exposes a JSON/HTTP API on port 1113 for job control
- **Volume management**: Manages ZFS datasets for persistent storage
- **Bootstrapping**: Coordinates initial cluster setup

Each container gets:
- A unique network address (via flannel overlay network)
- A cgroup with resource limits (default 1 GiB memory)
- A read-only squashfs root filesystem with an overlay for writes
- Access to persistent ZFS volumes when needed

## discoverd

Service discovery and cluster coordination. Every service instance registers with discoverd, which maintains a real-time directory of all services and their network addresses.

- Runs on every node (omni job)
- Backed by Raft consensus for consistency
- Provides DNS interface on port 53 (e.g., `leader.postgres.discoverd`)
- Supports leader election via service metadata
- Health checks for registered instances

## flannel

Overlay networking using VXLAN. Assigns each container a unique IP from a cluster-wide subnet. All containers can reach each other directly by IP regardless of which host they run on.

## controller

The central API server for Flynn. The CLI and dashboard communicate exclusively with the controller.

Key concepts:

- **App**: A named application with configuration (env vars, routes, scale)
- **Release**: An immutable snapshot of an app's code (artifact) + configuration
- **Artifact**: A reference to a container image (squashfs layers in blobstore)
- **Formation**: The desired scale of each process type for a release
- **Deployment**: A managed transition from one release to another

The controller stores all state in PostgreSQL.

## scheduler

Determines which host should run each job. Distributes work across the cluster by job count (not resource-aware). Handles:

- Scaling formations up/down
- Replacing crashed jobs
- Omni jobs (one per host)
- Rolling deployments

## router

HTTP and TCP load balancer. Routes external traffic to app containers based on domain name (HTTP) or port (TCP).

- TLS termination with automatic certificate management
- Sticky sessions support
- WebSocket support
- Health-check-based backend selection

## Build Pipeline

### Git Push Flow

1. `git push flynn master` → gitreceive accepts the push
2. gitreceive triggers slugbuilder with the pushed code
3. slugbuilder detects the language and runs the appropriate Heroku buildpack
4. The resulting "slug" (compiled app) is stored in blobstore
5. Controller creates a new release pointing to the slug
6. Scheduler deploys the new release (rolling deployment)

### Docker Push Flow

1. `flynn docker push` uploads image layers to blobstore
2. Controller creates an artifact and release
3. Scheduler deploys the new release

## Database Architecture

Database appliances use the **sirenia** state machine for high availability:

```
┌─────────┐    sync repl    ┌─────────┐    async repl    ┌─────────┐
│ Primary │───────────────▶│  Sync   │───────────────▶│  Async  │
└─────────┘                 └─────────┘                 └─────────┘
     │                           │                           │
     └───── discoverd state ─────┴───────────────────────────┘
```

- State transitions are coordinated via discoverd service metadata
- Synchronous replication to the sync replica ensures zero data loss on failover
- The sync promotes to primary when the primary fails
- Each database type (PostgreSQL, MariaDB, MongoDB) implements the sirenia `Database` interface

## Storage

- **Blobstore**: Stores slugs, Docker layers, and other binary artifacts. Backends: local filesystem, S3, GCS, Azure Blob
- **ZFS volumes**: Persistent storage for databases. Supports snapshots for backups
- **Squashfs layers**: Read-only filesystem layers for container root filesystems. Stacked with overlay mounts

## Security

- All inter-component communication uses TLS with certificate pinning
- TUF (The Update Framework) provides secure artifact distribution with offline key signing
- Database credentials are generated per-app and stored in the controller
- Container isolation via Linux namespaces and cgroups

## Network Architecture

```
External Traffic
      │
      ▼
┌──────────┐
│  Router  │ (ports 80/443)
└──────────┘
      │
      ▼ (flannel overlay network)
┌──────────┐  ┌──────────┐  ┌──────────┐
│ App Pod 1│  │ App Pod 2│  │ App Pod 3│
└──────────┘  └──────────┘  └──────────┘
```

Each node's router instance receives external traffic and forwards to app containers via the flannel overlay. DNS resolution for internal services uses discoverd (port 53).
