---
title: Production
order: 7
---

This guide covers running Flynn in production.

## Cluster Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| Nodes | 3 | 3-5 |
| RAM per node | 8 GB | 16 GB |
| CPU per node | 4 cores | 8 cores |
| Storage per node | 40 GB (ZFS) | 100 GB+ SSD (ZFS) |
| OS | Ubuntu 24.04 LTS | Ubuntu 24.04 LTS |
| Network | Full mesh TCP/UDP | Low-latency, same datacenter |

## Storage

Flynn uses ZFS for all persistent storage. Use dedicated SSDs for the ZFS pool:

```bash
sudo zpool create -f flynn-default mirror /dev/sdb /dev/sdc
```

Mirror or raidz configurations are recommended for production data integrity.

## Blobstore Backend

By default, the blobstore uses local ZFS storage. For production, configure an external object store:

### Amazon S3

```bash
flynn -a blobstore env set \
  BACKEND=s3 \
  S3_BUCKET=my-flynn-blobstore \
  S3_REGION=us-east-1 \
  AWS_ACCESS_KEY_ID=... \
  AWS_SECRET_ACCESS_KEY=...
```

### Google Cloud Storage

```bash
flynn -a blobstore env set \
  BACKEND=gcs \
  GCS_BUCKET=my-flynn-blobstore \
  GCS_CREDENTIALS='...'
```

### Azure Blob Storage

```bash
flynn -a blobstore env set \
  BACKEND=azure \
  AZURE_CONTAINER=my-flynn-blobstore \
  AZURE_STORAGE_ACCOUNT=... \
  AZURE_STORAGE_KEY=...
```

## DNS and Load Balancing

### DNS

Configure a wildcard DNS record pointing to all cluster nodes:

```
*.flynn.example.com  A  10.0.0.1
*.flynn.example.com  A  10.0.0.2
*.flynn.example.com  A  10.0.0.3
```

### External Load Balancer

For production, place a load balancer (HAProxy, nginx, AWS ALB) in front of the Flynn router:

- Forward ports 80 and 443 to all cluster nodes
- Use TCP mode (the Flynn router handles TLS termination)
- Enable health checks against the router's health endpoint

## Firewall

### External Access

| Port | Protocol | Source | Purpose |
|------|----------|--------|---------|
| 80 | TCP | Public | HTTP traffic |
| 443 | TCP | Public | HTTPS traffic |
| 22 | TCP | Admin IPs | SSH |

### Inter-Node (Allow Between All Cluster Nodes)

| Port | Protocol | Purpose |
|------|----------|---------|
| 1111 | TCP | discoverd |
| 1113 | TCP | flynn-host API |
| 5002 | TCP+UDP | flannel |
| 53 | TCP+UDP | DNS |
| 3000-3500 | TCP | Internal services |
| 49152-65535 | TCP | Container ports |

## Backups

### Cluster Backup

```bash
flynn cluster backup --file cluster-backup.tar
```

This exports all apps, releases, formations, routes, and database contents.

### Cluster Restore

```bash
flynn cluster restore --file cluster-backup.tar
```

### Database Backups

Individual database backups:

```bash
flynn -a my-app pg dump > pg-backup.sql
flynn -a my-app pg restore < pg-backup.sql
```

### Automated Backups

Schedule regular backups with cron:

```bash
0 */6 * * * flynn cluster backup --file /backups/flynn-$(date +\%Y\%m\%d-\%H\%M).tar
```

## Monitoring

### Health Checks

- Controller API: `GET /status` on the controller
- Router health: check port 80/443 response
- discoverd: `GET /services` on port 1111
- flynn-host: `GET /host/status` on port 1113

### Logs

Flynn aggregates logs from all containers via the log aggregator service:

```bash
flynn -a my-app log -f
```

For external log shipping, configure a syslog drain.

## Updating

### Rolling Update

1. Back up the cluster
2. Update `flynn-host` binary on each node
3. Run `flynn-host download` to fetch new component images
4. Restart `flynn-host` on each node (one at a time for zero-downtime)
5. The scheduler will redeploy system apps with new images

### Version Pinning

Flynn uses TUF for version management. Pin to a specific version:

```bash
flynn-host download --version v20260416.0
```

## Adding/Removing Hosts

### Adding a Host

1. Install Flynn on the new node
2. Start `flynn-host daemon` with `--peer-ips` pointing to existing nodes
3. The new host joins the cluster automatically

### Removing a Host

```bash
# Drain jobs from the host
flynn-host drain <HOST_ID>

# Remove from cluster
flynn-host remove <HOST_ID>
```

## Security Considerations

- Rotate the controller key periodically
- Use TLS for all external communication
- Keep ZFS pools encrypted at rest where possible
- Restrict SSH access to admin IPs only
- Use firewall rules to limit inter-node ports to cluster members only
