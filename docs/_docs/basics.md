---
title: Basics
order: 2
---

This guide assumes you have a running Flynn cluster and the `flynn` CLI configured. If not, follow the [Installation Guide](/docs/installation/) first.

## Deploy an App

Flynn deploys applications via `git push`. Create a Flynn app in your project directory:

```bash
cd my-app/
flynn create my-app
```

This adds a `flynn` git remote. Deploy by pushing:

```bash
git push flynn master
```

Flynn detects your language, builds the app using a buildpack, and starts it.

## View Your App

After deployment, your app is available at:

```
http://my-app.<CLUSTER_DOMAIN>
```

Open it in your browser or check with curl:

```bash
flynn open
# or
curl http://my-app.demo.localflynn.com
```

## Configuration

Set environment variables for your app:

```bash
flynn env set DATABASE_URL=postgres://...
flynn env set SECRET_KEY=mysecret
```

View current configuration:

```bash
flynn env
```

Unsetting a variable:

```bash
flynn env unset SECRET_KEY
```

Setting env vars triggers a new deployment automatically.

## Scaling

View current process formation:

```bash
flynn scale
```

Scale web processes:

```bash
flynn scale web=3
```

Scale multiple types:

```bash
flynn scale web=3 worker=2
```

## Logs

View app logs:

```bash
flynn log
```

Follow logs in real time:

```bash
flynn log -f
```

Filter by process type:

```bash
flynn log -t web
```

## Running One-Off Commands

Execute a command in your app's environment:

```bash
flynn run bash
flynn run python manage.py migrate
flynn run rails console
```

## Process Management

List running processes:

```bash
flynn ps
```

Kill a specific process:

```bash
flynn kill <JOB_ID>
```

## Routes

View current routes:

```bash
flynn route
```

Add a custom domain:

```bash
flynn route add http my-app.example.com
```

## Releases

View deployment history:

```bash
flynn release show
```

Roll back to a previous release:

```bash
flynn release rollback
```

## Procfile

Define process types in a `Procfile` at the root of your project:

```
web: node server.js
worker: node worker.js
clock: node scheduler.js
```

Flynn uses this to determine what processes to run.
