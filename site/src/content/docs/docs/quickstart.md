---
title: Quickstart
description: Run the skael platform and connect the CLI in under two minutes.
---

## 1. Run the platform

If you already have Postgres, the only required env var is `DATABASE_URL`:

```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/skael?sslmode=disable" \
  -v skael-data:/data \
  ghcr.io/skael-dev/skael:latest
```

Migrations run automatically on startup. The volume holds the master key the server generates on first start; keep it with your Postgres backups.

The platform is at `http://localhost:8080`. The first sign-up asks for the claim code, which the server prints to its log (`docker logs <container>`). Sign up to create the owner account and a personal API key. (Port 8080 in use? Stop whatever holds it, or change the left side of `-p 8080:8080`.)

No Postgres handy? Use Docker Compose, which bundles one:

```bash
docker compose up -d
```

This brings up the server and database only. Publishing, scanning and syncing work immediately. Skill evaluations need a separate `skael-worker` running on the host — see [Self-hosting](/docs/self-hosting#evaluation-worker-optional).

## 2. Install the CLI

```bash
# macOS / Linux (curl installer — fastest)
curl -fsSL skael.dev/install | sh

# macOS (Homebrew)
brew install --cask skael-dev/skael/skael

# Windows (PowerShell, no admin)
irm https://skael.dev/install.ps1 | iex

# Windows (Scoop)
scoop bucket add skael https://github.com/skael-dev/scoop-skael
scoop install skael

# From source
git clone https://github.com/skael-dev/skael.git
cd skael && go build -o skael ./cmd/skael
```

The PowerShell installer puts `skael.exe` in `%LOCALAPPDATA%\Programs\skael\bin` and adds that folder to your user `PATH`. Open a new terminal after it finishes. Hooks on Windows need no Git Bash, WSL or jq.

`go install github.com/skael-dev/skael/cmd/skael@latest` does not work — `go.mod` has a `replace` directive, and `go install <pkg>@version` refuses any module carrying one. Building from a clone is unaffected.

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
