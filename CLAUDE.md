# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Skael

Skael is a self-hostable platform + CLI for managing AI agent skills (SKILL.md files) across engineering teams. It provides a central registry, cross-agent sync, security scanning, and activation tracking. See `docs/prd.md` for the full product context.

## Commands

Uses [just](https://github.com/casey/just) as the task runner. Run `just` to see all commands. Key ones:

```bash
just build                    # build binaries to bin/ (server, skael, whetstone, skael-worker)
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

Three binaries from one Go module (`github.com/skael-dev/skael`):

**`cmd/server`** — HTTP API server. Chi router + Huma v2 (auto-generates OpenAPI spec). Embeds a React SPA via `embed.FS` from `web/dist/`. Auth via user accounts (bcrypt passwords, session cookies) + personal API keys (SHA-256, `X-API-Key` header). Middleware stack: security headers, request ID, CORS, per-route-class rate limiting, request logging. Auth middleware skips `/api/health`, `/api/health/ready`, `/api/openapi.json`, `/api/capabilities`, `/api/auth/signup`, `/api/auth/login`, `/api/auth/logout`, and `/metrics`. Subcommand `reset-password --email` for operator-run password recovery (requires direct database access; run by whoever operates the server, not the `admin` role).

**`cmd/skael`** — CLI. Cobra commands, Lipgloss styling. Talks to the server API via `cli/client/`. Config at `~/.skael/config.json`, sync state at `~/.skael/state.json`.

**`cmd/skael-worker`** — Eval queue worker. Polls `POST /api/eval/jobs/claim`, materialises a local whetstone workspace from the claimed job's skill bundle and suite, runs the evaluation against a Docker sandbox, heartbeats the lease while running, and posts the report back. The LLM judge always runs through the direct Anthropic API gateway (`ANTHROPIC_API_KEY`), never a subscription CLI on PATH. Panel execution is not the same guarantee: the claude-code agent adapter declares `AuthDirs: ["~/.claude", "~/.config/claude"]`, which `internal/eval/runner/session.go` mounts into every sandbox, so a panel member authenticates with whatever host credentials it finds there — subscription-backed wherever those directories exist on the worker's host. One poll loop, one job at a time per process; run more replicas for throughput. `checkAdapters` at startup asserts every agent adapter blank-import actually registered, since a forgotten import compiles clean but silently thins the panel.

### Package layout

Package names say what most packages hold; read the package doc comment for the rest. Listed here only where the *why* is not visible from the code:

- `internal/scan/` — A `Rule` may carry a `Reject` regex that suppresses matches, because RE2 has no lookahead. Every line is scanned raw *and* NFKC-normalized with zero-width/bidi chars stripped, so unicode obfuscation can't evade; line-pair scanning catches secrets split across lines. `shellast.go` parses shell with `mvdan.cc/sh` and matches dangerous constructs *structurally*, so spacing and line-splitting can't hide them.
- `internal/server/` — `RegisterAPIRoutes` (`routes.go`) is the one place every `/api/*` group is registered, and both `Builder.Build()` and `--openapi` call it, so the generated spec cannot drift from what the server serves. Register new groups *inside* it: the guard test only sees routes registered there.
- `internal/gate/` — `Decide` takes no database, HTTP, context or clock, so every policy question is answerable by a table test. Appealability is **per-rule**, not per-category: `Rule.Class` overrides the class derived from `Category`, which is how an RCE cradle (`curl … | bash`) and credential-path *access* stay appealable while data actually leaving the machine does not.
- `internal/evalqueue/` — Claim/lease/heartbeat, so a worker dying mid-job doesn't strand it; a lapsed lease returns the job to the pool.
- `internal/evalsuite/` — Suites are content-addressable archives stored beside skill bundles, so a score can be re-run later against the exact same tasks with a different panel. A suite may be authored (whetstone) or machine-derived from a skill's own `SKILL.md`; `Origin` records which.
- `internal/quality/` — Only ever ingests a report the worker posts back through `internal/evalqueue`. There is no direct write path.
- `internal/worker/` — Docker and the LLM gateway are injected by `cmd/skael-worker`, not imported here, which is what makes the run loop testable without either.
- `internal/ownership/` — `Resolve` (exact → longest prefix → unowned, replace-not-stack) and `CanManage` are pure. `Resolver` folds instance privilege into `IsOwner` at the boundary so `gate.Decide` stays pure.
- `internal/analytics/` — Events carry an `event_source` because agents measure activations differently; unregistered skill names are counted separately from activations.

### Conventions

- **Comment density: follow `internal/skill` and `internal/platform` (~10% of lines), not `internal/eval`.** The eval packages run 20–60% and are the outlier, not the target — treating them as the anchor produces files that are half prose. Comment the non-obvious decision, not the function: an import cycle, a security constraint, a "do not 'fix' this" (`drift.Aggregate`'s deliberate ÷N), a failure mode that is silent. Do not restate what the next line does, and do not repeat the same rationale in more than one place — state it once, nearest the code that depends on it, and let the other sites be brief.
- **Commit messages: a subject line plus a short paragraph of *why*.** Detail belongs in the PR body, which is where reviewers read it. A commit body that runs to several screens is a design document in the wrong place.

### Key patterns

- **Huma v2 routes:** JSON endpoints use `huma.Register(api, huma.Operation{...}, handler)`. Binary endpoints (download, scan results) use Chi router directly.
- **`skill.RegisterRoutes` takes `(api huma.API, router chi.Router, store *Store, storage platform.Storage, opts RouteOptions)`** — it needs both the Huma API and the underlying Chi router. `RouteOptions.Queue`/`.Suites` are local interfaces (`QueueSubmitter`, `SuiteLookup`) rather than concrete `evalqueue`/`evalsuite` types: `internal/skill` cannot import either package because both of them import `internal/skill` for their own route wiring, and Go doesn't allow the reverse. `internal/server` (`evalQueueAdapter`, `evalSuiteAdapter` in `server.go`) bridges the two, since it already imports everything.
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
- **The generated SDK is untracked.** `web/openapi.json` and `web/src/api/` are gitignored — run `just generate` before type-checking or `npx vitest run`, and never `git add -f` them. CI regenerates them in every job that needs them.
- **`npx tsc --noEmit` does not type-check the app.** It resolves the root `web/tsconfig.json`; the real settings live in `web/tsconfig.app.json`, which sets `noUncheckedIndexedAccess: true`. The build script is `tsc -b && vite build`, and only `tsc -b` applies them. A whole feature branch once passed `--noEmit` on every task and then failed `npm run build` with 10 errors — which CI runs (`ci.yml`), so it would have been red on push. **Verify with `npx tsc -b` or `npm run build`.**
- **Huma drops unexported embedded structs from responses.** `SchemaLinkTransformer` rebuilds a response body by reflection and copies only exported fields, and an anonymous embedded field's exportedness comes from its *type* name. Embedding an unexported wire struct yields a 200 with a near-empty body and no error anywhere. Any embedded response type must be exported (this is why `quality.RecordOutput` is).
- **`tests/e2e/` is behind `//go:build integration`.** `go build ./...` and `go vet ./...` do not compile it, so a changed function signature can break it invisibly. Run `go vet -tags=integration ./...` after any signature change.
- **A Huma request-body field without `,omitempty` is required.** Adding one to an existing endpoint returns 422 to every already-deployed client. Validate optional enums by hand in the handler instead of with an `enum` tag, so an omitted value can default.
- **Postgres intervals take an integer:** `make_interval(days => $1)`. Building one by string concatenation (`($1 || ' days')::interval`) makes Postgres infer a text parameter that pgx cannot encode an int into, and the query fails at runtime only.
- **`skills.tags`, `author`, `license`, `compatibility` and `spec_compliance` are written *only* by `UpdateSpecFields`.** Creating a skill does not populate them. Any new path that creates a skill must call it, or that skill is invisible to tag filtering and the tag list endpoint.
- **A version number is not the latest pointer.** `skills.latest_version` points at the newest *released* version; `skill_versions.version` is `MAX(version)+1`. A version held by the publish gate has a number and an archive but does not advance the pointer, which is what keeps it out of list, search, sync and download-latest. Anything that infers one from the other will serve a held version. The guarantee is "nothing servable is served", not "a held version is invisible everywhere": `Store.List`/`Search` have never filtered on `latest_version`, so a skill whose only version is held appears with `latest_version: 0`, exactly like any created-but-unpublished skill. That is correct and deliberate — no archive, content, or scan result leaks.
- **`skael publish`'s local pre-scan calls `gate.Decide`, not a severity threshold.** It aborts before upload only on a `Block`; an appealable finding is uploaded, held server-side, and reported as held. Reintroducing a local `critical || high` check would make the entire review path unreachable from the CLI without `--skip-local-scan`, which is how it was missed once already. `cli/` imports `internal/gate` directly — `gate` depends only on `internal/scan`, so there is no cycle and no reason to duplicate the rules.
- **A server-side scan runs against a throwaway unpack directory.** `scan.ScanDir` reports whatever path it was handed, so findings from `internal/skill`'s publish route and `internal/import` would otherwise name `/tmp/skael-publish-…/SKILL.md` — meaningless to the publisher, persisted into `scan_result`, and a disclosure of the server's filesystem layout. Both call sites run `scan.Relativize(report, dir)` before the report is decided on, stored, or returned. Any new server-side scan site must too.
- **Always test with `-count=1`.** Go caches passing results, and a cached pass has already produced one false verification here.
- **Test package conventions differ:** `cli/client/client_test.go` is `package client` (internal, so it can reach unexported helpers); most others are `package X_test`. Check before adding a file.
- **The hook script tests execute real bash** (`cli/hooks/script_exec_test.go`), with a fake `curl` on `PATH`, and wait on the script's process group rather than a timeout — the scripts background their POST and `disown` it. The file is `//go:build unix`.
- **`cmd/server` holds no Docker socket and no LLM key — both live on `cmd/skael-worker`.** Neither `cmd/server` nor `internal/server` imports anything Docker- or Anthropic-related; `cmd/skael-worker` is the only binary that does (`internal/eval/sandbox/docker`, `ANTHROPIC_API_KEY`). That's a deliberate boundary, not an oversight — it's why evaluation is a queue the server enqueues to and the worker drains, rather than something the server runs inline. A change that makes the server execute an eval directly breaks it.
- **A hold is a set of reasons, not a state.** `skill_versions.hold_reasons` lists what must clear; `version_approvals` records who cleared what; `gate_state` is derived. A verified quality score clears only `scan` and must never clear `ownership` — if it could, the entire review path is decorative. An owner clears only `ownership` and must never clear `scan` — if they could, the security gate is as weak as the least careful self-managed namespace.
- **`ReasonScan` and `ReasonOwnership` are persisted wire names**, in `hold_reasons` and in `version_approvals.reason`. Renaming one makes every in-flight hold permanently unclearable.
- **Ownership never gates reads and never re-gates a released version.** Deleting a user, removing a rule, or transferring a namespace changes who reviews future changes and nothing else. A skill that worked yesterday keeps working for everyone who synced it.
- **Unowned does not hold a publish** — only a *matched rule* does. That is what makes an upgrade a no-op for an existing install: protection switches on per namespace when someone writes their first rule. Do not "fix" this into holding every unowned publish; it floods the review queue on upgrade day.
- **`go install <pkg>@version` cannot work here, so install docs must not offer it.** `go.mod` ends with `replace github.com/charmbracelet/x/ansi => …v0.10.1` (commit `5cceef4`: minio-go's graph re-resolves ansi to v0.11.7, which broke lipgloss's pinned cellbuf), and `go install` refuses any module carrying a replace directive. It fails for *every* binary under `cmd/`, not just one, and no existing tag can ever be fixed — the replace is in the published go.mod. Building from a clone is unaffected, which is what the docs offer instead. Nothing in CI covers the install paths, which is how this stayed broken across several releases.
- **Homebrew formulas are generated from `.goreleaser.yaml`, not edited in the tap.** `skael-dev/homebrew-skael` is a publish target; its `.rb` files say "DO NOT EDIT". One `brews:` entry per binary — `skael` and `whetstone` — rather than one archive carrying several, because `install.sh` derives its download name from a single `BINARY` and the docs describe each archive as holding one binary.
- **Trajectory paths must be workspace-relative before they reach the drift engine.** `runner/session.go` calls `trajectory.Relativize` on every session's and probe's events. Skipping it breaks the drift score in *both* directions at once — coverage falls to zero because nothing matched, while violation and order rise to a vacuous 1.0 because nothing could be violated either — producing a constant that reads like a measurement. `drift.Result.Unmeasurable` guards it.
- **`InstallSkill` installs shipped content only**, filtered by `lint.Excluded`. The directory it is handed is the authoring skill dir, which holds every task's `oracle/solve.sh`; unfiltered, the reference solution lands in the workspace of the agent being measured.
- **`Report.Headline` (0–100, minimum member Effectiveness) is the only composite** and the only published score. A drift letter grade and a bootstrapped CI were removed; `DriftGrade` remains as an always-empty `omitempty` field so existing decoders keep working.
- **There are two independent model axes, with two different gateways.** The *judge* runs in-process through `llm.Gateway`, is asked for a model by class (never by name), and is pointed somewhere by `LLM_BASE_URL`. The *panel* — the agents that attempt the tasks — runs a CLI inside the sandbox and is pointed somewhere by `ANTHROPIC_BASE_URL`, which the claude-code adapter forwards in. `runner.DefaultPanel()` asks for the bare aliases `opus`/`haiku`, which only Anthropic's own API resolves, so a BYOK gateway that namespaces its ids 404s every panel member. That is why setting `ANTHROPIC_BASE_URL` (and *not* `LLM_BASE_URL`) is what switches the panel's models over to `LLM_STRONG_MODEL`/`LLM_FAST_MODEL`. Wiring the panel to the judge's gateway is the easy mistake; `TestPanelModels` in `cmd/skael-worker` guards it.
- **The panel's two model overrides apply together or not at all.** Substituting only one slot yields a panel with one working member and one that 404s — which is not an error but a *complete run* that scores, reports `PanelComplete: false`, and therefore can never release the version it was meant to clear, having spent a full tier to find out. Keeping both members on the same footing means a half-configured gateway fails both health probes and is caught by `checkPanelHealth` before an eval row is even created.
- **`llm.ClassFast` is never requested in production code** — every real judge call asks for `ClassStrong`. `LLM_FAST_MODEL` therefore does nothing for the judge today; its only live effect is selecting the panel's floor member behind a custom gateway. Anyone who set it to something cheap "because it did nothing" will now see it evaluated, and `RobustnessGap` move.
- **`platform.MigrateUpTo` exists so a migration is tested against a database populated at the prior version.** A test that opens a fully-migrated database and then "upgrades" it passes with the migration deleted — which is exactly what both pre-existing migration tests here did.
- **`suite.Check` stages task workspaces under `CheckOptions.StageRoot`, and a containerized caller must set it.** Those directories are bind-mounted into sibling sandbox containers, so the path must resolve identically for the process and for the Docker daemon — the same constraint `WORKER_RUN_ROOT` already carries for session workspaces, and the same silent failure if it is wrong: Docker creates a missing bind source as an empty directory, so every oracle and verifier runs against an empty workspace and voids every task with no error anywhere. Empty means `os.TempDir()`, which is correct only on an author's host.
- **A derived suite is not an authored one, and `eval_suites.origin` is what says so.** A suite generated from a skill's own `SKILL.md` grades the skill against its own claims, so `skill.Releaser.Reconsider` refuses to clear a `scan` hold on a score against one. The server stamps `origin` at report time from the job's empty `suite_ref` — never from anything the worker declares — and a re-run against a stored derived suite stays derived, which is why the check reads `job.SuiteRef == "" || suite.Origin == "derived"` rather than either half alone.

## Server env vars

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `STORAGE_PATH` | no | `./data/skills` | Archive storage; local dir, or `s3://bucket/prefix` for S3 |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `DISABLE_SIGNUP` | no | `false` | Set to `true` (the literal string) to lock signups after setup. The server logs a startup warning when unset |
| `S3_ENDPOINT` / `S3_REGION` | no | `s3.amazonaws.com` / `us-east-1` | Only when `STORAGE_PATH=s3://…`. Only `S3_REGION` falls back to `AWS_REGION`; the endpoint has no `AWS_*` fallback |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | no | — | Both fall back to `AWS_*`. The pair is all-or-nothing: set only one and it is silently ignored. Omit both for an IAM instance role |
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
| `LOG_FORMAT` | no | — | Set to `pretty` for colorized console output (development). Unset or anything else logs JSON, for production/log aggregation |
| `LOG_PRETTY` | no | `false` | Set to `true` for the same colorized console output as `LOG_FORMAT=pretty`; either one triggers it |
| `COOKIE_SECURE` | no | `false` | Set to `true` to mark the session cookie `Secure` (requires a TLS-terminating reverse proxy in front — the browser refuses a `Secure` cookie over plain HTTP, which silently breaks login). The server logs a startup warning when unset |
| `RATE_LIMIT_AUTH` | no | `20` | Per-minute request budget for `/api/auth/*`, keyed by IP only (unauthenticated by definition) |
| `RATE_LIMIT_EVENTS` | no | `600` | Per-minute budget for `POST /api/events`, keyed by API key where present, else IP |
| `RATE_LIMIT_READ` | no | `300` | Per-minute budget for GET/HEAD routes (list, search, manifest, downloads), keyed by API key where present, else IP |
| `RATE_LIMIT_WRITE` | no | `60` | Per-minute budget for all other mutating routes (publish, import, delete), keyed by API key where present, else IP |
| `RATE_LIMIT_SUITES` | no | `20` | Per-minute budget for `POST /api/eval/suites` (accepts up to a base64-encoded 10MB archive per call — decode, unpack, storage write, DB insert), keyed by API key where present, else IP |
| `METRICS_ENABLED` | no | `true` | Set to `false` to disable the `/metrics` Prometheus endpoint |
| `GITHUB_TOKEN` | no | — | GitHub personal access token for import; raises API rate limits |
| `QUALITY_FLOOR` | no | `0` | Minimum headline quality score a verified evaluation must reach to release a version held for review. `0` accepts any verified report with a complete panel and no critical contract violations. |

Auth is via user accounts + personal API keys (no static server key). `DISABLE_SIGNUP=true` locks signups after setup.

## Worker env vars

`cmd/skael-worker` reads its own environment, independent of the server's `.env`:

| Variable | Required | Default | Description |
|---|---|---|---|
| `SKAEL_ENDPOINT` | yes | — | Base URL of the Skael server the worker claims jobs from |
| `SKAEL_API_KEY` | yes | — | API key the worker authenticates with |
| `ANTHROPIC_API_KEY` | yes | — | Direct Anthropic API gateway key for the LLM judge, which never falls back to a subscription CLI on PATH. Panel execution is separate: agent adapters with `AuthDirs` (e.g. claude-code) mount host credential directories into the sandbox and are subscription-backed wherever those exist |
| `WORKER_ID` | no | `{hostname}-{pid}` | Identifies this worker in job leases |
| `WORKER_LEASE` | no | `5m` | How long a claimed job's lease lasts before it's considered abandoned (Go duration) |
| `WORKER_POLL` | no | `15s` | Interval between claim attempts when the queue is empty (Go duration) |
| `WORKER_WORK_ROOT` | no | `os.TempDir()` | Directory to materialise eval workspaces under. Never bind-mounted into a sandbox, so it does not need to be host-visible |
| `WORKER_RUN_ROOT` | only in a container | `os.TempDir()` | Directory per-session sandbox workspaces are created under. These **are** bind-mounted into sandbox containers, so the path must resolve identically for the worker and for the daemon starting them. Required when containerized — the worker refuses to start without it. The suite deriver's oracle gate (`suite.Check`) stages its task workspaces here too, for the same bind-mount reason |
| `WORKER_CONCURRENCY` | no | `1` | Must be a positive integer |
| `LLM_BASE_URL` | no | `https://api.anthropic.com` | Point the judge gateway at an OpenRouter-compatible endpoint |
| `LLM_AUTH_STYLE` | no | `x-api-key` | Either `x-api-key` (Anthropic) or `bearer`. Any other value fails startup |
| `LLM_STRONG_MODEL` | no | `claude-opus-5` | Judge model. **Also** the eval panel's strong member when `ANTHROPIC_BASE_URL` is set — see below |
| `LLM_FAST_MODEL` | no | `claude-haiku-4-5-20251001` | Cheaper model for the gateway's fast path. **Also** the eval panel's floor member when `ANTHROPIC_BASE_URL` is set |
| `ANTHROPIC_BASE_URL` | no | — | The *panel's* gateway, forwarded into every sandbox by the claude-code adapter — not to be confused with `LLM_BASE_URL`, which is the *judge's*. Setting it switches the panel's models over to `LLM_STRONG_MODEL`/`LLM_FAST_MODEL` |

Unlike the server, the worker's duration and integer parsing does **not** silently fall back — a malformed `WORKER_LEASE`, `WORKER_POLL`, `WORKER_CONCURRENCY`, or `LLM_AUTH_STYLE` fails startup with the offending value named.

### Running the worker in a container

Supported, and how `docker compose --profile worker up` runs it (`Dockerfile.worker`). The worker starts sandbox containers as *siblings* through a mounted `/var/run/docker.sock` — not Docker-in-Docker. Two constraints follow, and both are load-bearing:

- **`WORKER_RUN_ROOT` must be bind-mounted at the same path on both sides** (`-v /var/lib/skael/run:/var/lib/skael/run`). Session workspaces are bind-mounted into each sandbox and the *host* daemon resolves that path. A container-local path names nothing on the host, and Docker creates a missing bind source as an empty directory rather than failing — so the sandbox starts with no task and no skill and scores as a skill that did nothing. `requireRunRoot` in `cmd/skael-worker` refuses to start containerized without it, because nothing downstream can distinguish that from a genuinely bad skill.
- **Auth must arrive as environment variables, not as a mounted credential directory.** `runner.resolveAuth` prefers an adapter's `AuthEnv` over its `AuthDirs` and returns *no* mounts when any of them is set, which is what removes the other class of host-path bind. Mounting `~/.claude` is the one auth style that stays host-only — subscription-backed panel execution still works containerized via `CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`), which is a token rather than a directory. `docker-compose.yml` carries all three supported setups (Anthropic key, subscription panel, OpenRouter) with two commented out.

Kubernetes needs the same two things — a `hostPath` volume with `mountPath` equal to `path`, and the node's Docker socket. That second requirement is real: a node running containerd rather than Docker has no socket for this driver to talk to, so the worker belongs on a Docker node (or on the host) until a containerd/Kubernetes sandbox driver exists.

Each of the events/read/write/suites classes also enforces a shared per-IP ceiling — `ipCeilingFactor` (10) × that class's limit — checked before the per-key budget, so one source address can't exceed it no matter how many distinct API keys it presents; raising the class's env var raises the ceiling proportionally.

## Security constraints

These exist for good reasons — don't weaken them without understanding why:

- `storage.Write/Read/Delete` validate paths stay within `BasePath` (path traversal prevention)
- `Unpack` rejects symlinks, hardlinks, unknown tar entry types, files >1MiB, and total extraction >50MB
- `MaxBytesReader` middleware caps request bodies at 10MB (must be < `maxUnpackSize`)
- Scanner runs on publish — `critical` and `warn` (high severity) block publishing
- Exfiltration and secret class findings are unappealable — no evaluation, no admin override, no suppression clears them. They return 422 and create no version row at all. Every other blocking finding holds the version for review instead, where a verified score at or above `QUALITY_FLOOR` or an owner/admin `skael review --approve` releases it
- API key hashes are compared via `crypto/subtle.ConstantTimeCompare` (after SHA-256 + hex.DecodeString) to prevent timing attacks
- `X-Forwarded-For` / `X-Real-IP` are read only when the connecting peer is inside `TRUSTED_PROXIES`; otherwise the socket address wins. Both headers are plain client input, and believing them unconditionally (as chi's deprecated `RealIP` does) lets one attacker present a fresh source address per request, defeating every per-IP limit
- Hook scripts read credentials from `~/.skael/config.json` at runtime — never embedded in agent config files
- Sync verifies downloaded archive checksums against the manifest before extracting
- File permissions are masked to `0o777` during extraction (no setuid/setgid)
- `GET /api/users/search` is open to any authenticated user and returns `{id, name, email}` only — no role, no timestamps. Minimum 2 characters and a hard cap of 20 results, so it is a lookup rather than a directory export. Restricting it to admins would make delegated ownership unusable, which is the trade being made
- `ownership.CanManage` is the entire escalation surface of ownership. It permits narrowing only; a delegate can never widen their scope. The property test in `internal/ownership/manage_test.go` is the guard — three individually-correct clauses can compose into a widening path that no per-clause test can see
- `DELETE /api/skills/{name}` has no role check and no ownership check — any authenticated member can delete a skill's every version and archive, while being unable to publish a single line to it. This is pre-existing (not introduced by ownership) and a known gap for a follow-up, not an oversight
