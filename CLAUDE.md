# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Skael

Skael is a self-hostable platform + CLI for managing AI agent skills (SKILL.md files) across engineering teams. It provides a central registry, cross-agent sync, security scanning, and activation tracking. See `docs/prd.md` for the full product context.

## Commands

```bash
# Build
make build                    # produces bin/skael-server and bin/skael
go build ./cmd/server         # server only
go build ./cmd/skael          # CLI only

# Test
go test ./... -v -count=1     # all unit + component tests (needs Docker for testcontainers)
go test ./internal/scan/ -v   # single package
go test ./internal/skill/ -v -run TestStore_Create -count=1  # single test

# Integration / E2E
go test -tags integration ./tests/e2e/ -v -count=1 -timeout 120s

# Run locally
docker compose up -d          # start platform + Postgres
DATABASE_URL=postgres://skael:skael@localhost:5432/skael API_KEY=sk-dev go run ./cmd/server
```

## Architecture

Two binaries from one Go module (`github.com/skael-dev/skael`):

**`cmd/server`** — HTTP API server. Chi router + Huma v2 (auto-generates OpenAPI spec). Embeds a React SPA via `embed.FS` (Plan 3, not yet built). Auth is a single API key checked via `X-API-Key` header; middleware skips `/api/health` and `/api/openapi.json`.

**`cmd/skael`** — CLI. Cobra commands, Lipgloss styling. Talks to the server API via `cli/client/`. Config at `~/.skael/config.json`, sync state at `~/.skael/state.json`.

### Package layout

- `internal/skill/` — Core domain. `Store` (Postgres CRUD + versioning), `RegisterRoutes` (Huma endpoints), `Pack`/`Unpack` (tar.gz archives), `ParseFrontmatter` (YAML), `Search` (FTS + pg_trgm).
- `internal/scan/` — Security scanner. Regex rules in `secrets.go`, `injection.go`, `exfiltration.go`, `obfuscation.go`. `ScanDir` walks a directory; `ScanContent` scans a single file. Line-pair scanning catches secrets split across lines.
- `internal/analytics/` — Activation tracking. `POST /api/events` ingests hook events; `GET /api/skills/{name}/activations` returns per-skill summary with agent breakdown.
- `internal/platform/` — Infrastructure. `Config` (env vars), `NewPool` + `RunMigrations` (pgx + embedded SQL), `Storage` (local filesystem with path traversal validation).
- `internal/auth/` — `Middleware(apiKey)` returns Chi middleware with constant-time key comparison.
- `internal/sync/` — `GetManifest()` query joining skills + latest versions for sync diffing.
- `internal/ui/` — Lipgloss styles and output helpers (`Success`, `Error`, `Warn`, `Download`, `Summary`). `JSONMode` flag suppresses styled output; commands write JSON to stdout instead.
- `cli/` — Cobra commands (one file per command). `cli/client/` is the HTTP client, `cli/config/` handles `~/.skael/`, `cli/agents/` detects installed agents, `cli/hooks/` manages activation tracking hook scripts.

### Key patterns

- **Huma v2 routes:** JSON endpoints use `huma.Register(api, huma.Operation{...}, handler)`. Binary endpoints (download, scan results) use Chi router directly.
- **`skill.RegisterRoutes` takes `(api huma.API, router chi.Router, store *Store, storage *platform.Storage)`** — it needs both the Huma API and the underlying Chi router.
- **Testcontainers:** DB-backed tests use `testutil.SetupTestDB(t)` which spins up Postgres 17 per test. Each test gets a fresh migrated database.
- **Content-addressable archives:** Published archives are stored at `{skillName}/{checksum[:16]}.tar.gz`, not by version number. This prevents race conditions on concurrent publishes.
- **Skill names:** Must match `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, max 128 chars.

## Server env vars

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `API_KEY` | yes | — | API key for authentication |
| `STORAGE_PATH` | no | `./data/skills` | Local directory for archives |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |

## Security constraints

These exist for good reasons — don't weaken them without understanding why:

- `storage.Write/Read/Delete` validate paths stay within `BasePath` (path traversal prevention)
- `Unpack` rejects symlinks, hardlinks, unknown tar entry types, files >1MiB, and total extraction >50MB
- `MaxBytesReader` middleware caps request bodies at 10MB (must be < `maxUnpackSize`)
- Scanner runs on publish — `critical` and `warn` (high severity) block publishing
- Hook scripts read credentials from `~/.skael/config.json` at runtime — never embedded in agent config files
- Sync verifies downloaded archive checksums against the manifest before extracting
- File permissions are masked to `0o777` during extraction (no setuid/setgid)
