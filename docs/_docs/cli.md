---
title: CLI Reference
order: 6
---

The `flynn` command-line tool manages apps, deployments, and cluster configuration.

## Installation

Download the latest release from the [GitHub releases page](https://github.com/consolving/flynn/releases).

Binaries are available for the following platforms:

| Platform | File |
|----------|------|
| Linux (amd64) | `flynn-cli-linux-amd64.gz` |
| Linux (arm64) | `flynn-cli-linux-arm64.gz` |
| macOS (Intel) | `flynn-cli-darwin-amd64.gz` |
| macOS (Apple Silicon) | `flynn-cli-darwin-arm64.gz` |
| Windows (amd64) | `flynn-cli-windows-amd64.exe.gz` |

### Linux / macOS

Replace `<VERSION>` with the desired release tag (e.g. `v20260503.0`) and `<PLATFORM>` with the appropriate file name:

```bash
VERSION="v20260503.0"

# Linux amd64
curl -fsSL "https://github.com/consolving/flynn/releases/download/${VERSION}/flynn-cli-linux-amd64.gz" | \
  gunzip > flynn && chmod +x flynn && sudo mv flynn /usr/local/bin/

# Linux arm64
curl -fsSL "https://github.com/consolving/flynn/releases/download/${VERSION}/flynn-cli-linux-arm64.gz" | \
  gunzip > flynn && chmod +x flynn && sudo mv flynn /usr/local/bin/

# macOS (Intel)
curl -fsSL "https://github.com/consolving/flynn/releases/download/${VERSION}/flynn-cli-darwin-amd64.gz" | \
  gunzip > flynn && chmod +x flynn && sudo mv flynn /usr/local/bin/

# macOS (Apple Silicon)
curl -fsSL "https://github.com/consolving/flynn/releases/download/${VERSION}/flynn-cli-darwin-arm64.gz" | \
  gunzip > flynn && chmod +x flynn && sudo mv flynn /usr/local/bin/
```

### Windows

Download `flynn-cli-windows-amd64.exe.gz` from the [releases page](https://github.com/consolving/flynn/releases), extract it, and add the resulting `flynn-cli-windows-amd64.exe` to your PATH.

## Cluster Configuration

```bash
# Add a cluster
flynn cluster add --tls-pin <PIN> <NAME> <DOMAIN> <KEY>

# List clusters
flynn cluster

# Remove a cluster
flynn cluster remove <NAME>

# Set default cluster
flynn cluster default <NAME>
```

## App Commands

| Command | Description |
|---------|-------------|
| `flynn create <name>` | Create a new app |
| `flynn apps` | List all apps |
| `flynn info` | Show app details |
| `flynn delete --yes` | Delete the current app |

## Deployment

| Command | Description |
|---------|-------------|
| `git push flynn master` | Deploy via git |
| `flynn docker push <image>` | Deploy a Docker image |
| `flynn release show` | Show current release |
| `flynn release rollback` | Roll back to previous release |
| `flynn deployment` | Show deployment status |
| `flynn deployment cancel` | Cancel in-progress deployment |

## Process Management

| Command | Description |
|---------|-------------|
| `flynn scale` | Show current formation |
| `flynn scale web=N` | Scale process type |
| `flynn ps` | List running jobs |
| `flynn kill <ID>` | Kill a job |
| `flynn run <cmd>` | Run a one-off command |
| `flynn log` | View logs |
| `flynn log -f` | Follow logs |

## Configuration

| Command | Description |
|---------|-------------|
| `flynn env` | List env vars |
| `flynn env set K=V` | Set env var(s) |
| `flynn env get K` | Get a specific var |
| `flynn env unset K` | Unset a var |

## Resources (Databases)

| Command | Description |
|---------|-------------|
| `flynn resource` | List provisioned resources |
| `flynn resource add <provider>` | Provision a database |
| `flynn resource remove <provider>` | Remove a resource |
| `flynn pg psql` | PostgreSQL console |
| `flynn pg dump` | PostgreSQL dump |
| `flynn mysql console` | MariaDB console |
| `flynn mongodb mongo` | MongoDB shell |
| `flynn redis redis-cli` | Redis CLI |

## Routes

| Command | Description |
|---------|-------------|
| `flynn route` | List routes |
| `flynn route add http <domain>` | Add HTTP route |
| `flynn route remove <ID>` | Remove a route |

## Flags

Most commands support these flags:

- `-a <app>` — Specify app name (default: detected from git remote)
- `-c <cluster>` — Specify cluster name

## Getting Help

```bash
flynn help
flynn help <command>
```
