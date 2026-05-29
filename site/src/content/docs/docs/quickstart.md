---
title: Quickstart
description: Run the skael platform and connect the CLI in under two minutes.
---

## 1. Run the platform

If you already have Postgres, the only required env var is `DATABASE_URL`:

```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/skael?sslmode=disable" \
  ghcr.io/skael-dev/skael:latest
```

Migrations run automatically on startup. The platform is at `http://localhost:8080` — sign up to create the first account and a personal API key.

No Postgres handy? Use Docker Compose, which bundles one:

```bash
docker compose up -d
```

## 2. Install the CLI

```bash
# macOS / Linux (Homebrew)
brew install skael-dev/skael/skael

# From source
go install github.com/skael-dev/skael/cmd/skael@latest
```

## 3. Connect

```bash
skael setup http://localhost:8080 <your-api-key>
```

This validates the connection, saves your config, detects installed agents (Claude Code, Cursor, Codex, OpenCode), runs the first sync, and installs activation-tracking hooks for each.

## 4. Publish a skill

```bash
skael publish ./code-review
```

The skill is scanned, packed, and uploaded. Next time anyone runs `skael sync`, they get it.
