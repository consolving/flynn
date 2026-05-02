---
title: Security
order: 9
---

## TLS

All communication between Flynn components and external clients uses TLS. The cluster generates a self-signed CA certificate during bootstrap. Clients verify the connection using a TLS pin (SHA-256 hash of the certificate).

```bash
flynn cluster add --tls-pin <PIN> ...
```

## TUF (The Update Framework)

Flynn uses TUF for secure distribution of component images:

- **4 root keys** (ed25519) with a 2-of-4 signing threshold
- **Offline root signing**: Root keys are kept offline; only targets/snapshot/timestamp keys are used in CI
- **Repository**: `https://consolving.github.io/flynn-tuf-repo/repository`
- **Artifacts**: GitHub Releases (`consolving/flynn` tag `v20260416.0`)

`flynn-host download` verifies all artifacts against TUF metadata before installation.

## Container Isolation

Each container runs with:

- Separate PID, mount, network, UTS, and IPC namespaces
- cgroup resource limits (memory, CPU)
- Read-only root filesystem (squashfs with overlay)
- Dropped capabilities (containers run with minimal privilege set)
- Separate network interface via flannel

## Authentication

- **Controller API**: Authenticated via a shared key (`CONTROLLER_KEY`)
- **Databases**: Per-app credentials generated at provisioning time
- **flynn-host API**: TLS client certificate verification between hosts
- **discoverd**: Cluster-internal, accessible only via flannel network

## Network Security

- Components communicate over the flannel overlay network (VXLAN)
- The router is the only externally-exposed service (ports 80/443)
- discoverd, flynn-host API, and other internal services are not accessible from outside the cluster

## Recommendations

1. Run cluster nodes in a private network (VPC)
2. Use firewall rules to restrict inter-node ports to cluster members
3. Store cluster backup encryption keys separately from backups
4. Rotate the controller key after initial setup
5. Monitor for unexpected processes via `flynn-host ps`
6. Keep the cluster updated with the latest TUF-verified images
