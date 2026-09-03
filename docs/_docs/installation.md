---
title: Installation
order: 1
---

Flynn runs on **Ubuntu 24.04 LTS (Noble)** amd64 servers. We recommend at least 4 GB RAM, 40 GB storage, and 2 CPU cores per node.

## Quick Start (Single Node)

For development or testing, a single-node cluster is the fastest way to get started.

### Prerequisites

- Ubuntu 24.04 LTS (amd64)
- Root access
- ZFS support (`zfsutils-linux` package)

### Install Flynn

Install runtime dependencies first:

```bash
sudo apt-get update
sudo apt-get install -y zfsutils-linux iptables curl squashfs-tools
```

The `flynn-host` and `flynn-init` binaries are currently distributed as hashed
TUF targets rather than as plain GitHub Release assets. The easiest way to
obtain them is to build from source; the CLI is available from GitHub Releases.

```bash
# Install build dependencies
sudo apt-get install -y golang-go libc6-dev libseccomp-dev

# Build flynn-host and flynn-init from the source tree
go build -o /tmp/flynn-host ./host
go build -o /tmp/flynn-init ./host/flynn-init

sudo mv /tmp/flynn-host /tmp/flynn-init /usr/local/bin/
```

Alternatively, download the current hashed targets directly from the TUF
repository (the hash changes with every release; check `targets.json` for the
current value):

```bash
# Top-level flynn-host.gz target (hashed filename)
curl -fsSL https://consolving.github.io/flynn-tuf-repo/repository/targets/v20260902.0/c5285f69d8160b0872b2b1a0dbd015a72922c626b3befa8c4bd065b72abc7e76d0d82a85100a39e352dded86641bbdc740e30d4e99503f4a9444fcd21623c9a3.flynn-host.gz | \
  gunzip > /tmp/flynn-host && chmod +x /tmp/flynn-host && sudo mv /tmp/flynn-host /usr/local/bin/

# Versioned flynn-init.gz target (v20260902.0)
curl -fsSL https://consolving.github.io/flynn-tuf-repo/repository/targets/v20260902.0/644d59669a178a84331d09253c43f1f7c4a05d3d0860477cc35be282b7197966d4e36f7067605e29f8f61032ab54929f0d65b4161aef322d73d1671318209b43.flynn-init.gz | \
  gunzip > /tmp/flynn-init && chmod +x /tmp/flynn-init && sudo mv /tmp/flynn-init /usr/local/bin/
```

### Configure ZFS

Flynn requires a ZFS pool for container storage:

```bash
# Create a ZFS pool (adjust device as needed)
# Option A: Use a dedicated disk
sudo zpool create -f flynn-default /dev/vdb

# Option B: Use a file-backed pool (development only)
sudo truncate -s 30G /var/lib/flynn-zpool.img
sudo zpool create -f flynn-default /var/lib/flynn-zpool.img
```

### Start flynn-host

```bash
# Download component images from TUF repository
sudo flynn-host download \
  --tuf-db /var/lib/flynn/tuf.db \
  --repository https://consolving.github.io/flynn-tuf-repo/repository \
  --root-keys-file /usr/local/share/flynn/root_keys.json

# Start the flynn-host daemon
sudo flynn-host daemon \
  --id node1 \
  --force \
  --init-path /usr/local/bin/flynn-init
```

### Bootstrap the Cluster

In a separate terminal:

```bash
sudo flynn-host bootstrap \
  --min-hosts=1 \
  --peer-ips=<NODE_IP>
```

The bootstrap process outputs credentials. Save the `CONTROLLER_KEY` and `TLS_PIN` values — you'll need them to configure the CLI.

### Install the CLI

```bash
curl -fsSL https://github.com/consolving/flynn/releases/download/v20260503.0/flynn-cli-linux-amd64.gz | \
  gunzip > /tmp/flynn && chmod +x /tmp/flynn && sudo mv /tmp/flynn /usr/local/bin/flynn
```

### Configure the CLI

```bash
flynn cluster add \
  --tls-pin <TLS_PIN> \
  default <CLUSTER_DOMAIN> <CONTROLLER_KEY>
```

## Multi-Node Cluster (Production)

For production, deploy at least 3 nodes for high availability. Flynn rejects `--min-hosts=2` — use 1 (singleton) or 3+.

### Requirements

| Component | Per Node |
|-----------|----------|
| OS | Ubuntu 24.04 LTS amd64 |
| RAM | 8 GB minimum |
| CPU | 4 cores |
| Disk | 40 GB (ZFS pool) |
| Network | Full TCP/UDP connectivity between nodes |

### Key Ports

| Port | Service |
|------|---------|
| 1111 | discoverd |
| 1113 | flynn-host API |
| 5002 | flannel |
| 53 | DNS |
| 80/443 | Router (HTTP/HTTPS) |

### Setup

1. Install Flynn on all 3 nodes (same steps as single-node)
2. Start `flynn-host daemon` on each node with unique `--id` values
3. Bootstrap from any node:

```bash
sudo flynn-host bootstrap \
  --min-hosts=3 \
  --peer-ips=<NODE1_IP>,<NODE2_IP>,<NODE3_IP>
```

### DNS Configuration

Point a wildcard DNS record to your node IPs:

```
*.flynn.example.com → <NODE1_IP>, <NODE2_IP>, <NODE3_IP>
```

Set `CLUSTER_DOMAIN=flynn.example.com` during bootstrap.

## Vagrant (Development)

A Vagrantfile is provided for local development using libvirt/KVM:

```bash
cd vagrant/
vagrant up  # Starts a single-node Flynn cluster
```

Requires: Vagrant, vagrant-libvirt plugin, libvirt, KVM.

## TUF Repository

Flynn uses [The Update Framework (TUF)](https://theupdateframework.io/) for secure artifact distribution. The TUF repository is hosted at:

- **Metadata**: `https://consolving.github.io/flynn-tuf-repo/repository` (and the IPFS-backed mirror `https://tuf.consolving.net/repository`)
- **CLI**: GitHub Releases (`consolving/flynn` tag `v20260503.0`)
- **Host/init binaries**: Hashed TUF targets under `repository/targets/`

Root keys use ed25519 with a 2-of-4 threshold for root role signing.
