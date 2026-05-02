---
title: Apps
order: 3
---

## Creating Apps

```bash
flynn create my-app
```

This creates a Flynn app and adds a `flynn` git remote to your repo.

To specify a custom remote name:

```bash
flynn create my-app --remote production
```

## Deployment

### Git Push

The standard deployment method:

```bash
git push flynn master
```

Flynn uses [Heroku buildpacks](https://devcenter.heroku.com/articles/buildpacks) to detect and build your application. Supported languages include Go, Node.js, Python, Ruby, Java, and PHP.

### Custom Buildpacks

Specify a custom buildpack via `.buildpacks` file or environment variable:

```bash
flynn env set BUILDPACK_URL=https://github.com/heroku/heroku-buildpack-nodejs
```

Or create a `.buildpacks` file for multi-buildpack support:

```
https://github.com/heroku/heroku-buildpack-nodejs
https://github.com/heroku/heroku-buildpack-ruby
```

### Zero-Downtime Deploys

Flynn performs rolling deployments by default:

1. New processes are started
2. Health checks pass
3. Old processes are gracefully stopped
4. Traffic is shifted to new processes

### Deploy Timeout

If a deploy is taking too long, cancel it:

```bash
flynn deployment cancel
```

### Git Branches

Only pushes to `master` trigger a deployment. To deploy a different branch:

```bash
git push flynn my-branch:master
```

## Configuration

### Environment Variables

```bash
# Set variables
flynn env set KEY=value ANOTHER=value2

# Get a specific variable
flynn env get KEY

# Unset a variable
flynn env unset KEY

# List all variables
flynn env
```

### External Databases

To use an external database instead of Flynn's built-in appliances, set the appropriate environment variable:

```bash
flynn env set DATABASE_URL=postgres://user:pass@external-host:5432/mydb
```

## Process Types

Define process types in a `Procfile`:

```
web: ./server --port $PORT
worker: ./worker
scheduler: ./cron
```

The `web` process type is special — it receives HTTP traffic via the router. Other process types run as background workers.

### Scaling

```bash
# View current scale
flynn scale

# Scale processes
flynn scale web=4 worker=2

# Scale to zero
flynn scale worker=0
```

## Resource Limits

Each process runs in a cgroup with resource limits. Default memory limit is 1 GiB per process.

## Domains and Routing

```bash
# List routes
flynn route

# Add HTTP route
flynn route add http app.example.com

# Add HTTPS route (with Let's Encrypt or custom cert)
flynn route add http --certificate /path/to/cert.pem --key /path/to/key.pem app.example.com

# Remove route
flynn route remove <ROUTE_ID>
```

## App Management

```bash
# List all apps
flynn apps

# Delete an app
flynn delete --yes

# View app info
flynn info
```
