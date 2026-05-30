# Skael

One registry for your team's AI skills — across every agent and every project.

Your team's SKILL.md files live in scattered home directories and half-synced repos, copied by hand into each agent, with no idea which ones are actually used. Skael is the single source of truth: publish once, and it versions every skill, scans it for secrets and prompt injection, syncs it to Claude Code, Cursor, Codex, and OpenCode on every machine, and shows you which skills actually fire — by which agent, how often. Self-hosted and open source.

![The skael dashboard — skills registry with versions, security status, and activation counts](site/public/dashboard-skills.png)

## Why not just a git repo?

You can commit `.claude/skills/` to a repo — if everyone's on the same agent, in the same project, and remembers to pull. A git folder gives you a folder. It doesn't place skills into Cursor *and* Codex *and* OpenCode, doesn't sync across machines, doesn't scan for injection, doesn't tell you which version everyone's on, and has no idea which skills your agents actually use. Skael is the layer that turns a folder of markdown into managed infrastructure — and unlike Claude's native org sharing (Claude.ai/Desktop, paid tiers only), it's vendor-neutral across every agent your team runs.

## Quick Start

### Run the published image (bring your own Postgres)

If you already have a Postgres database, the only required env var is `DATABASE_URL`:

```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/skael?sslmode=disable" \
  ghcr.io/skael-dev/skael:latest
```

Migrations run automatically on startup. Platform is at `http://localhost:8080` — sign up to create the first account and a personal API key.

### Self-hosted (Docker Compose)

Bundles Postgres, so there's nothing external to provision:

```bash
docker compose up -d
```

Platform is at `http://localhost:8080`.

> **Storage:** archives default to local disk (`STORAGE_PATH`). For Kubernetes/ephemeral hosts or multiple replicas, set `STORAGE_PATH=s3://bucket/prefix` to use S3-compatible object storage (AWS S3, MinIO, R2, Spaces) — see [Self-hosting](https://skael.dev/docs/self-hosting).

### Install the CLI

```bash
# macOS / Linux (Homebrew)
brew install skael-dev/skael/skael

# From source
go install github.com/skael-dev/skael/cmd/skael@latest
```

### Connect to your registry

```bash
skael setup http://localhost:8080 <your-api-key>
```

This validates the connection, saves config, syncs all skills, and installs activation tracking hooks for every detected agent.

## What it does

```bash
skael publish ./my-skill    # publish a skill to the registry
skael sync                   # pull latest skills to all your agents
skael scan ./my-skill        # security scan before publishing
skael search "review"        # find skills
skael list                   # see everything published
skael doctor                 # check your setup
skael hook install           # set up activation tracking
```

Every `skael publish` runs a security scan that checks for hardcoded secrets, prompt injection, data exfiltration patterns, dangerous shell commands, and obfuscated payloads. Critical and high-severity findings block publishing.

Every agent that uses a skill reports activation events back to the platform. `skael doctor` shows you which agents have tracking installed.

## Development

Requires: Go 1.25+, Docker, [just](https://github.com/casey/just)

```bash
cp .env.example .env         # configure local env vars
just db                      # start Postgres
just dev                     # run the server
just test                    # run all tests
just test-fast               # run tests without testcontainers (instant)
just test-e2e                # run end-to-end scenario tests
just check                   # vet + fmt + test
```

Run `just` to see all available commands.

### Project structure

```
cmd/server/     → API server binary (Huma v2 + Chi + Postgres)
cmd/skael/      → CLI binary (Cobra + Lipgloss)
internal/       → Server packages (skill, scan, analytics, auth, platform, sync)
cli/            → CLI packages (commands, client, config, agents, hooks)
tests/e2e/      → End-to-end integration tests
```

### Key commands

| Command | What it does |
|---|---|
| `just build` | Build both binaries to `bin/` |
| `just dev` | Run server with hot reload (reads `.env`) |
| `just db` | Start Postgres 17 in Docker |
| `just test` | All tests (needs Docker for testcontainers) |
| `just test-pkg internal/scan` | Test a single package |
| `just test-run TestScan_Clean` | Run a single test |
| `just test-fast` | Fast tests only (no DB, instant) |
| `just test-e2e` | End-to-end scenario tests |
| `just check` | Full CI check (vet + fmt + test) |
| `just scan ./path` | Security scan a skill directory |

## Architecture

Single Go binary embeds the API server and (soon) a React dashboard. Backed by Postgres for skill metadata, full-text search, and activation events. Skill archives stored on local filesystem.

The CLI is a separate binary that talks to the API. It handles agent detection, file placement, hook installation, and manifest-based sync with checksum verification.

## License

Apache-2.0
