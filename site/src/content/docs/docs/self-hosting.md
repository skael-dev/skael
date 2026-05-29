---
title: Self-hosting
description: Run the skael platform on your own infrastructure.
---

skael is a single Go binary that embeds the dashboard and serves the API. It needs one thing: a Postgres database.

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `STORAGE_PATH` | no | `./data/skills` | Local directory for skill archives |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `API_KEY` | no | — | Deprecated legacy auth; prefer user accounts + personal keys |

Migrations run automatically on startup.

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

Skill archives are stored on the local filesystem under `STORAGE_PATH`. Mount a volume there for persistence. Paths are validated to stay within the storage root.
