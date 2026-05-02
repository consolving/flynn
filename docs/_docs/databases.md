---
title: Databases
order: 4
---

Flynn includes built-in database appliances with automatic provisioning, authentication, and high availability. Each database runs as a managed service within the cluster.

## Available Databases

| Database | Version | HA Mode | Protocol |
|----------|---------|---------|----------|
| PostgreSQL | 16 | Primary + Sync + Async | PostgreSQL wire protocol |
| MariaDB | 10.11 LTS | Primary + Sync + Async | MySQL wire protocol |
| MongoDB | 4.4 | Replica Set | MongoDB wire protocol |
| Redis | 7.x | Single instance | Redis protocol |

## Provisioning

Add a database to your app:

```bash
# PostgreSQL (default)
flynn resource add postgres

# MariaDB
flynn resource add mysql

# MongoDB
flynn resource add mongodb

# Redis
flynn resource add redis
```

This creates a dedicated database, user, and password, then sets environment variables on your app.

## Environment Variables

After provisioning, Flynn sets these variables automatically:

### PostgreSQL

```
DATABASE_URL=postgres://user:pass@leader.postgres.discoverd:5432/dbname
PGDATABASE=dbname
PGHOST=leader.postgres.discoverd
PGUSER=user
PGPASSWORD=pass
```

### MariaDB

```
DATABASE_URL=mysql://user:pass@leader.mariadb.discoverd:3306/dbname
MYSQL_HOST=leader.mariadb.discoverd
MYSQL_USER=user
MYSQL_PWD=pass
MYSQL_DATABASE=dbname
```

### MongoDB

```
MONGO_HOST=leader.mongodb.discoverd
MONGO_USER=user
MONGO_PWD=pass
MONGO_DATABASE=dbname
```

### Redis

```
REDIS_URL=redis://:password@leader.redis-CLUSTER_ID.discoverd:6379
REDIS_HOST=leader.redis-CLUSTER_ID.discoverd
REDIS_PORT=6379
REDIS_PASSWORD=password
```

## High Availability

PostgreSQL, MariaDB, and MongoDB use the **sirenia** state machine for high availability. In a 3+ node cluster:

- **Primary**: Handles all reads and writes
- **Sync replica**: Synchronous standby, receives all writes before they're acknowledged
- **Async replica(s)**: Asynchronous standbys for read scaling and additional failover candidates

### Failover

When the primary fails:
1. The sync replica detects the failure via discoverd
2. The sync promotes to primary
3. An async replica promotes to sync
4. The cluster continues with no data loss (synchronous replication ensures the sync has all committed data)

State transitions are coordinated via discoverd service metadata to prevent split-brain.

### Singleton Mode

In a single-node cluster, databases run in singleton mode — one instance with no replication. This is suitable for development but not production.

## PostgreSQL

PostgreSQL 16 with full extension support.

### Connecting

```bash
flynn pg psql
```

Or connect from your app using `DATABASE_URL`.

### Extensions

Common extensions are available:

```sql
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "hstore";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
```

### Backups

```bash
flynn pg dump > backup.sql
flynn pg restore < backup.sql
```

## MariaDB

MariaDB 10.11 LTS, compatible with MySQL clients and drivers.

### Connecting

```bash
flynn mysql console
```

### Notes

- Uses InnoDB as the default storage engine
- Binary logging enabled for replication
- Authentication uses `mysql_native_password` for broad client compatibility

## MongoDB

MongoDB 4.4 with replica set configuration.

### Connecting

```bash
flynn mongodb mongo
```

### Notes

- Uses WiredTiger storage engine
- Authentication via SCRAM-SHA-1
- Each provisioned database gets a dedicated user with `dbOwner` role
- Replica set name: `rs0`

## Redis

Redis with password authentication.

### Connecting

```bash
flynn redis redis-cli
```

### Persistence

Redis uses RDB snapshots for persistence. Data is saved to disk periodically based on the default save policy. For write-heavy workloads, consider that data written between snapshots may be lost if the process is killed abruptly.

### Notes

- Each `flynn resource add redis` creates a dedicated Redis cluster
- Password authentication is required
- Runs as a single instance (no Redis cluster mode)
