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

This starts the platform plus a Postgres container with a persistent volume. The platform is at `http://localhost:8080`; sign up to create your first account.

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
