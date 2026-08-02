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

Migrations run automatically on startup. The platform is at `http://localhost:8080` — sign up to create the first account and a personal API key. (Port 8080 in use? Stop whatever holds it, or change the left side of `-p 8080:8080`.)

No Postgres handy? Use Docker Compose, which bundles one:

```bash
docker compose up -d
```

This brings up the server and database only — publishing and scanning work immediately, but skill evaluations need a separate `skael-worker` (`docker compose --profile eval up -d`; see [Self-hosting](/docs/self-hosting#evaluation-worker-optional)).

## 2. Install the CLI

```bash
# macOS / Linux (curl installer — fastest)
curl -fsSL skael.dev/install | sh

# macOS / Linux (Homebrew)
brew install skael-dev/skael/skael

# From source
go install github.com/skael-dev/skael/cmd/skael@latest
```

## 3. Connect

```bash
skael setup http://localhost:8080 <your-api-key>
```

This validates the connection, saves your config, detects installed agents (Claude Code, Cursor, Codex, OpenCode), and installs activation-tracking and auto-sync hooks for each. Pass `--no-auto-sync` to skip auto-sync hook installation.

## 4. Install skills

```bash
skael add code-review
skael add my-team:debugging --scope project
```

Skills are installed explicitly — pick what you need from the registry. Use `--scope project` to install a skill only for the current project, or `--scope user` for all projects (default comes from your config).

## 5. Publish a skill

```bash
skael publish ./code-review
```

The skill is scanned, packed, and uploaded. Anyone who has it installed gets the update on their next `skael sync` (or automatically via auto-sync hooks).

## 6. Keep up to date

Auto-sync hooks run `skael sync` in the background with 30-minute debouncing — your agents always have the latest versions of installed skills without manual intervention. You can also run `skael sync` manually at any time.
