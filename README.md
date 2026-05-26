# Skael

Control plane for AI agent skills. Publish, sync, scan, and track SKILL.md files across your team's agents.

Skael gives engineering teams a central registry for the skills that power their AI coding agents (Claude Code, Codex CLI, Gemini CLI). It handles versioning, cross-agent distribution, security scanning, and activation tracking — so you know which skills are actually being used.

## Quick Start

### Self-hosted (Docker Compose)

```bash
cp .env.example .env          # set API_KEY and DATABASE_URL
docker compose up -d
```

Platform is at `http://localhost:8080`. Set `API_KEY` in `.env` before running in production.

### Install the CLI

```bash
# macOS / Linux (Homebrew)
brew install alternayte/skael/skael

# macOS / Linux (curl)
curl -fsSL https://raw.githubusercontent.com/alternayte/skael-releases/main/install.sh | sh

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
docs/           → PRD, SDD, UI/UX specs, implementation plans
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

See `docs/sdd.md` for the full system design and `docs/prd.md` for product requirements.

## License

TBD
