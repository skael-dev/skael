---
title: Self-hosting
description: Run the skael platform on your own infrastructure.
---

skael is a single Go binary that embeds the dashboard and serves the API. It needs one thing: a Postgres database.

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `STORAGE_PATH` | no | `./data/skills` | Archive storage: a local directory, or `s3://bucket/prefix` for S3 |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `TRUSTED_PROXIES` | no | — | Comma-separated addresses or CIDR blocks whose `X-Forwarded-For` / `X-Real-IP` are believed. Unset means neither header is trusted. Set it when running behind a reverse proxy — see [Production](/docs/production#telling-skael-about-the-proxy) |

Migrations run automatically on startup. Auth is via user accounts and personal API keys — there is no static server key; sign up to create the first account.

## Bring your own Postgres

```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/skael?sslmode=disable" \
  -v skael-data:/data/skills \
  -e STORAGE_PATH=/data/skills \
  ghcr.io/skael-dev/skael:latest
```

## Bundled Postgres (Docker Compose)

```bash
docker compose up -d
```

This starts the platform plus a Postgres container with a persistent volume. The platform is at `http://localhost:8080`; sign up to create your first account. Publishing and scanning work right away. Evaluations do not — that's a separate opt-in piece, see below.

## Evaluation worker (optional)

The server queues evaluation jobs but never runs them — no Docker socket and no LLM key live on it. Running evaluations requires a separate `skael-worker` process, because it needs a Docker daemon to sandbox each run and a direct Anthropic API key to judge the result.

Run it on the host, alongside Compose rather than inside it:

```bash
export SKAEL_ENDPOINT=http://localhost:8080
export SKAEL_API_KEY=<a personal API key with permission to claim eval jobs>
export ANTHROPIC_API_KEY=<your direct Anthropic API key>
skael-worker
```

**Why not a Compose service?** The worker bind-mounts its own working directory and the agent credential directories into each sandbox container, through the Docker socket. Those mounts are resolved by the host's Docker daemon. A worker running inside a container would hand the daemon paths that exist only in its own filesystem, so every sandbox would start with empty mounts and the panel would fail to authenticate. Running it on the host keeps those paths real.

### Worker configuration

Required — the worker exits at startup naming whichever is missing:

| Variable | Description |
|---|---|
| `SKAEL_ENDPOINT` | Base URL of the skael server the worker claims jobs from |
| `SKAEL_API_KEY` | API key the worker authenticates with |
| `ANTHROPIC_API_KEY` | Direct Anthropic API key for the judge model — never a subscription CLI on PATH |

Optional, with defaults:

| Variable | Default | Description |
|---|---|---|
| `WORKER_ID` | `{hostname}-{pid}` | Identifies this worker in job leases |
| `WORKER_LEASE` | `5m` | How long a claimed job's lease lasts before it's considered abandoned |
| `WORKER_POLL` | `15s` | Interval between claim attempts when the queue is empty |
| `WORKER_WORK_ROOT` | OS temp dir | Directory to materialise eval workspaces under |
| `WORKER_CONCURRENCY` | `1` | Must be a positive integer |

The judge model (`ANTHROPIC_API_KEY`) is checked at startup. The panel agents that attempt the tasks are separate and not checked at startup: the claude-code adapter mounts `~/.claude` and `~/.config/claude` from the worker's host into the sandbox, read-only, so it authenticates with whatever Claude Code login is there. If those directories don't exist, the worker still starts and claims jobs, but the panel agent has no credentials and can't run — the job comes back as an incomplete panel rather than an error. See [Quality scoring](/docs/quality) for more.

## Storage

By default, skill archives are stored on the local filesystem under `STORAGE_PATH` (paths are validated to stay within the storage root). In Docker/Kubernetes, mount a persistent volume there — otherwise archives are lost when the container restarts.

## Object storage (S3-compatible)

For ephemeral/k8s deployments or to run multiple replicas, point `STORAGE_PATH` at S3-compatible object storage (AWS S3, MinIO, Cloudflare R2, Backblaze B2, DigitalOcean Spaces):

```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/skael?sslmode=disable" \
  -e STORAGE_PATH="s3://my-bucket/skael" \
  -e S3_REGION="us-east-1" \
  -e S3_ACCESS_KEY_ID="..." -e S3_SECRET_ACCESS_KEY="..." \
  ghcr.io/skael-dev/skael:latest
```

| Variable | Notes |
|---|---|
| `STORAGE_PATH` | `s3://bucket/prefix` switches to S3; any other value is a local path |
| `S3_ENDPOINT` | default `s3.amazonaws.com`; set for MinIO/R2/Spaces |
| `S3_REGION` | falls back to `AWS_REGION`; default `us-east-1` |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | fall back to `AWS_*`; omit both to use an IAM instance role |
| `S3_USE_PATH_STYLE` | `true` for MinIO |
| `S3_USE_SSL` | default `true`; `false` for local MinIO |

The bucket must already exist. On AWS, omit the keys to use an EKS/ECS instance role.
