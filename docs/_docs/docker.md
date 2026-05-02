---
title: Docker
order: 5
---

Flynn supports deploying Docker images directly, without requiring a buildpack build step.

## Deploying a Docker Image

```bash
# Create the app
flynn create my-app

# Push a Docker image
flynn docker push my-registry.com/my-image:latest
```

Flynn pulls the image, creates a release, and deploys it.

## Local Images

Push a locally-built image:

```bash
docker build -t my-app .
flynn docker push my-app
```

## Port Configuration

Flynn routes HTTP traffic to the port specified by the `PORT` environment variable (default: `8080`). Your application should listen on `$PORT`:

```dockerfile
ENV PORT 8080
EXPOSE 8080
CMD ["./server", "--port", "8080"]
```

Or read the `PORT` env var dynamically:

```javascript
const port = process.env.PORT || 8080;
app.listen(port);
```

## Process Types

Define multiple process types using the `CMD` and labels, or specify them after push:

```bash
flynn scale web=2 worker=1
```

## Environment Variables

Set configuration the same way as buildpack apps:

```bash
flynn env set DATABASE_URL=postgres://...
```

## Example: Node.js

```dockerfile
FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
RUN npm install --production
COPY . .
ENV PORT 8080
EXPOSE 8080
CMD ["node", "server.js"]
```

```bash
docker build -t my-node-app .
flynn create my-node-app
flynn docker push my-node-app
```

## Limitations

- Private registry authentication is configured via `flynn docker set-login`
- Multi-stage builds work normally (only the final image is pushed)
- Images must be linux/amd64 architecture
