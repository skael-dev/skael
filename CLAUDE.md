# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Skael

Skael is a self-hostable platform + CLI for managing AI agent skills (SKILL.md files) across engineering teams. It provides a central registry, cross-agent sync, security scanning, and activation tracking. See `docs/prd.md` for the full product context.

## Commands

Uses [just](https://github.com/casey/just) as the task runner. Run `just` to see all commands. Key ones:

```bash
just build                    # build both binaries to bin/
just dev                      # run server (reads .env)
just db                       # start Postgres in Docker
just test                     # all tests (needs Docker for testcontainers)
just test-fast                # fast tests without DB (instant)
just test-pkg internal/scan   # single package
just test-run TestStore_Create # single test by name
just test-e2e                 # browser e2e (Playwright, needs dev server + db)
just test-integration         # Go integration e2e (real server + testcontainers)
just check                    # vet + fmt-check + test + integration e2e + web tests (CI)
just scan ./path              # security scan a skill directory
just migrate                  # run pending migrations
just migrate-down             # rollback last migration
just migrate-status           # show migration status
just migrate-create add_foo   # create a new migration file
```

Migrations use [goose](https://github.com/pressly/goose). SQL files live in `internal/platform/migrate/` with `-- +goose Up` and `-- +goose Down` annotations. Migrations run automatically on server startup and in tests via `testutil.SetupTestDB`.

Server reads config from `.env` (see `.env.example`). Copy it before first run: `cp .env.example .env`

## Architecture

Two binaries from one Go module (`github.com/skael-dev/skael`):

**`cmd/server`** — HTTP API server. Chi router + Huma v2 (auto-generates OpenAPI spec). Embeds a React SPA via `embed.FS` from `web/dist/`. Auth via user accounts (bcrypt passwords, session cookies) + personal API keys (SHA-256, `X-API-Key` header). Middleware stack: security headers, request ID, CORS, per-route-class rate limiting, request logging. Auth middleware skips `/api/health`, `/api/health/ready`, `/api/openapi.json`, `/api/capabilities`, `/api/auth/signup`, `/api/auth/login`, `/api/auth/logout`, and `/metrics`. Subcommand `reset-password --email` for operator-run password recovery (requires direct database access; run by whoever operates the server, not the `admin` role).

**`cmd/skael`** — CLI. Cobra commands, Lipgloss styling. Talks to the server API via `cli/client/`. Config at `~/.skael/config.json`, sync state at `~/.skael/state.json`.

### Package layout

- `internal/skill/` — Core domain. `Store` (Postgres CRUD + versioning), `RegisterRoutes` (Huma endpoints), `Pack`/`Unpack` (tar.gz archives), `ParseFrontmatter` (YAML), `Search` (FTS + pg_trgm).
- `internal/scan/` — Security scanner. Regex rules in `secrets.go`, `injection.go`, `exfiltration.go`, `obfuscation.go`, `execution.go`. A `Rule` may carry an optional `Reject` regex that suppresses matches (RE2 has no lookahead). `ScanDir` walks a directory; `ScanContent` scans a single file. Each line is scanned raw and (when it changes) NFKC-normalized with zero-width/bidi chars stripped, so unicode-obfuscated payloads can't evade. Line-pair scanning catches secrets split across lines. `shellast.go` (Phase 2) additionally parses shell scripts and fenced shell blocks in markdown with `mvdan.cc/sh` and detects dangerous constructs *structurally* (pipe-to-shell RCE, `eval` of dynamic content, `/dev/tcp` reverse shells) regardless of spacing/line-splitting/comments. Secret matches are always masked in the report; binary and oversized files are flagged (non-blocking) instead of silently skipped.
- `internal/analytics/` — Activation tracking. `POST /api/events` ingests hook events; `GET /api/skills/{name}/activations` returns per-skill summary with agent breakdown. Events carry an `event_source` (`tool_invocation` | `transcript_scan`) because agents measure activations differently; skill names are validated at ingest and unregistered names are counted separately from activations.
- `internal/platform/` — Infrastructure. `Config` (env vars), `NewPool` + `RunMigrations` (pgx + embedded SQL), `Storage` (local filesystem or S3 with path traversal validation), `middleware.go` (security headers, request ID), `clientip.go` (trust-aware client IP resolution — see `TRUSTED_PROXIES`), `ratelimit.go` (per-route-class limits keyed by API key, falling back to IP), `metrics.go` (Prometheus HTTP instrumentation), `logging.go` (request logger with health-check exclusion).
- `internal/auth/` — User accounts, sessions, API keys. `Middleware(sessionManager, userStore, keyStore)` enforces auth on `/api/` routes. Three roles: `owner` (first account created, sole role-granter, exactly one per instance), `admin` (granted by the owner; can override a blocked publish), `member` (default for every new account). `PUT /api/admin/users/{id}/role` (owner only) sets another user's role to `admin` or `member`; the owner's own role cannot be changed. `GET /api/admin/users` lists all users (owner only). Password reset: change own + `POST /api/admin/reset-password` (owner only) issues a temporary password.
- `internal/import/` — GitHub skill import. `POST /api/import` discovers and imports skills from GitHub repos. Uses `GITHUB_TOKEN` for API rate limits.
- `internal/server/` — Server assembly. `Builder` pattern wires stores, middleware, routes, and the embedded SPA. `Capabilities` endpoint (`/api/capabilities`) reports edition/features. `Readiness` check (`/api/health/ready`) verifies DB + storage. Enterprise extension points (`WithAuthorizer`, `WithExtraRoutes`).
- `internal/sync/` — `GetManifest()` query joining skills + latest versions for sync diffing.
- `internal/testutil/` — `SetupTestDB(t)` spins up Postgres 17 via testcontainers per test.
- `internal/ui/` — Lipgloss styles and output helpers (`Success`, `Error`, `Warn`, `Download`, `Summary`). `JSONMode` flag suppresses styled output; commands write JSON to stdout instead.
- `cli/` — Cobra commands (one file per command): `setup`, `list`, `search`, `show`, `publish`, `sync`, `scan`, `init`, `doctor`, `hook`, `import`, `add`, `remove`. `cli/client/` is the HTTP client (with retry), `cli/config/` handles `~/.skael/` (config.json tracks installed skills like package.json, state.json caches sync state), `cli/agents/` detects installed agents (Claude Code, Codex, OpenCode, Cursor), `cli/hooks/` manages activation tracking and auto-sync hook scripts.

### Key patterns

- **Huma v2 routes:** JSON endpoints use `huma.Register(api, huma.Operation{...}, handler)`. Binary endpoints (download, scan results) use Chi router directly.
- **`skill.RegisterRoutes` takes `(api huma.API, router chi.Router, store *Store, storage platform.Storage, ext ...*scan.ExternalScanner)`** — it needs both the Huma API and the underlying Chi router.
- **Testcontainers:** DB-backed tests use `testutil.SetupTestDB(t)` which spins up Postgres 17 per test. Each test gets a fresh migrated database.
- **Content-addressable archives:** Published archives are stored at `{skillName}/{checksum[:16]}.tar.gz`, not by version number. This prevents race conditions on concurrent publishes.
- **Skill names:** Must match `^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`, max 128 chars. Colons support namespaced names (e.g., `superpowers:brainstorming`).
- **Selective sync:** Skills are installed explicitly via `skael add`, tracked in `config.json`'s `skills` array. `skael sync` only updates installed skills (not the full registry). Legacy configs without a `skills` key are auto-migrated from `state.json` on first run.
- **Auto-sync hooks:** A debounced bash script (`~/.skael/hooks/skael-autosync.sh`) checks `state.json`'s `last_sync` timestamp — if <30 minutes old, it's a no-op. Installed as `UserPromptSubmit` (Claude Code), `sessionStart` (Cursor), `PreToolUse` (Codex — PascalCase; the event key in `config.toml` is `[[hooks.PreToolUse]]`, not `pre_tool_use`). Hook entries use `"_managed_by": "skael-autosync"` to distinguish from activation tracking hooks (`"_managed_by": "skael"`).
- **Activation measurement:** hooks report explicit skill invocations only. A skill that is read but not invoked carries no attribution. `event_source` distinguishes an agent reporting a tool call (`tool_invocation`) from a hook scanning a transcript afterwards (`transcript_scan`) — the two are not comparable (transcript scans can catch skills that were only read, and miss nothing an invocation would catch; tool invocations miss skills that were read but never invoked) — never sum across sources without labelling them.

## Gotchas

Each of these has already caused a real bug or a wasted debugging session.

- **Run the server, not just the tests.** Two shipped bugs were invisible to a green suite: the server could not boot on default config (see the chi note below), and `EVENT_RETENTION_DAYS` never purged anything. Both were obvious within seconds of starting the binary. Nothing except `cmd/server` calls `server.Build()`, so a startup defect passes every package test.
- **chi: every `router.Use(...)` must precede every route registration.** Registering a route and then calling `Use` panics at startup with "all middlewares must be defined before routes on a mux". This is why `/metrics` is mounted after the middleware stack is installed rather than inline.
- **The generated SDK is untracked.** `web/openapi.json` and `web/src/api/` are gitignored — run `just generate` before `npx tsc --noEmit` or `npx vitest run`, and never `git add -f` them. CI regenerates them in every job that needs them.
- **`tests/e2e/` is behind `//go:build integration`.** `go build ./...` and `go vet ./...` do not compile it, so a changed function signature can break it invisibly. Run `go vet -tags=integration ./...` after any signature change.
- **A Huma request-body field without `,omitempty` is required.** Adding one to an existing endpoint returns 422 to every already-deployed client. Validate optional enums by hand in the handler instead of with an `enum` tag, so an omitted value can default.
- **Postgres intervals take an integer:** `make_interval(days => $1)`. Building one by string concatenation (`($1 || ' days')::interval`) makes Postgres infer a text parameter that pgx cannot encode an int into, and the query fails at runtime only.
- **`skills.tags`, `author`, `license`, `compatibility` and `spec_compliance` are written *only* by `UpdateSpecFields`.** Creating a skill does not populate them. Any new path that creates a skill must call it, or that skill is invisible to tag filtering and the tag list endpoint.
- **Always test with `-count=1`.** Go caches passing results, and a cached pass has already produced one false verification here.
- **Test package conventions differ:** `cli/client/client_test.go` is `package client` (internal, so it can reach unexported helpers); most others are `package X_test`. Check before adding a file.
- **The hook script tests execute real bash** (`cli/hooks/script_exec_test.go`), with a fake `curl` on `PATH`, and wait on the script's process group rather than a timeout — the scripts background their POST and `disown` it. The file is `//go:build unix`.

## Server env vars

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `STORAGE_PATH` | no | `./data/skills` | Archive storage; local dir, or `s3://bucket/prefix` for S3 |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `S3_ENDPOINT` / `S3_REGION` | no | AWS / `us-east-1` | Only when `STORAGE_PATH=s3://…` |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | no | — | Fall back to `AWS_*`; omit both for IAM instance role |
| `S3_USE_PATH_STYLE` / `S3_USE_SSL` | no | `false` / `true` | `S3_USE_PATH_STYLE=true` for MinIO |
| `EVENT_RETENTION_DAYS` | no | `90` | Days of skill_events to keep; older rows are purged on startup. `0` disables cleanup |
| `EXTERNAL_SCAN_CMD` | no | — | Opt-in external scanner run on publish/import; `{dir}` → skill dir, must emit SARIF on stdout (e.g. `gitleaks dir {dir} --report-format sarif --report-path /dev/stdout`). Findings merge into the native scan. |
| `EXTERNAL_SCAN_TIMEOUT` | no | `60s` | Per-scan timeout for `EXTERNAL_SCAN_CMD` (Go duration) |
| `DB_MAX_CONNS` | no | `25` | Maximum number of connections in the pool |
| `DB_MIN_CONNS` | no | `5` | Minimum number of idle connections the pool maintains |
| `DB_MAX_CONN_LIFETIME` | no | `1h` | Maximum lifetime of a connection before it is closed (Go duration) |
| `DB_MAX_CONN_IDLE_TIME` | no | `30m` | Maximum idle time before a connection is closed (Go duration) |
| `DB_HEALTH_CHECK_PERIOD` | no | `1m` | Interval between pool health checks (Go duration) |
| `CORS_ORIGINS` | no | — | Comma-separated allowed origins for CORS (e.g. `https://app.skael.dev,http://localhost:5173`) |
| `TRUSTED_PROXIES` | no | — | Comma-separated CIDR blocks and/or bare addresses (IPv4 or IPv6) whose `X-Forwarded-For` / `X-Real-IP` may be believed, e.g. `10.0.0.0/8,192.168.1.5`. Empty (the default) ignores both headers and uses the socket address — correct for a directly-exposed server. Set it to the proxy's address when running behind one, or every client shares one rate-limit bucket |
| `LOG_LEVEL` | no | `info` | Zerolog level: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic` |
| `RATE_LIMIT_AUTH` | no | `20` | Per-minute request budget for `/api/auth/*`, keyed by IP only (unauthenticated by definition) |
| `RATE_LIMIT_EVENTS` | no | `600` | Per-minute budget for `POST /api/events`, keyed by API key where present, else IP |
| `RATE_LIMIT_READ` | no | `300` | Per-minute budget for GET/HEAD routes (list, search, manifest, downloads), keyed by API key where present, else IP |
| `RATE_LIMIT_WRITE` | no | `60` | Per-minute budget for all other mutating routes (publish, import, delete), keyed by API key where present, else IP |
| `RATE_LIMIT_SUITES` | no | `20` | Per-minute budget for `POST /api/eval/suites` (accepts up to a base64-encoded 10MB archive per call — decode, unpack, storage write, DB insert), keyed by API key where present, else IP |
| `METRICS_ENABLED` | no | `true` | Set to `false` to disable the `/metrics` Prometheus endpoint |
| `GITHUB_TOKEN` | no | — | GitHub personal access token for import; raises API rate limits |

Auth is via user accounts + personal API keys (no static server key). `DISABLE_SIGNUP=true` locks signups after setup.

Each of the events/read/write classes also enforces a shared per-IP ceiling — `ipCeilingFactor` (10) × that class's limit — checked before the per-key budget, so one source address can't exceed it no matter how many distinct API keys it presents; raising the class's env var raises the ceiling proportionally.

## Security constraints

These exist for good reasons — don't weaken them without understanding why:

- `storage.Write/Read/Delete` validate paths stay within `BasePath` (path traversal prevention)
- `Unpack` rejects symlinks, hardlinks, unknown tar entry types, files >1MiB, and total extraction >50MB
- `MaxBytesReader` middleware caps request bodies at 10MB (must be < `maxUnpackSize`)
- Scanner runs on publish — `critical` and `warn` (high severity) block publishing
- API key hashes are compared via `crypto/subtle.ConstantTimeCompare` (after SHA-256 + hex.DecodeString) to prevent timing attacks
- `X-Forwarded-For` / `X-Real-IP` are read only when the connecting peer is inside `TRUSTED_PROXIES`; otherwise the socket address wins. Both headers are plain client input, and believing them unconditionally (as chi's deprecated `RealIP` does) lets one attacker present a fresh source address per request, defeating every per-IP limit
- Hook scripts read credentials from `~/.skael/config.json` at runtime — never embedded in agent config files
- Sync verifies downloaded archive checksums against the manifest before extracting
- File permissions are masked to `0o777` during extraction (no setuid/setgid)
