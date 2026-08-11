---
title: Self-hosting
description: Run the skael platform on your own infrastructure.
---

skael is a single Go binary that embeds the dashboard and serves the API. It needs one thing: a Postgres database.

## Configuration

Every variable the server reads. `DATABASE_URL` is the only required one; everything else has a working default. S3 settings are separate — see [Object storage](#object-storage-s3-compatible).

Migrations run automatically on startup. Auth is via user accounts and personal API keys — there is no static server key; sign up to create the first account.

### Core

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `STORAGE_PATH` | no | `./data/skills` | Archive storage: a local directory, or `s3://bucket/prefix` for S3 |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `DISABLE_SIGNUP` | no | `false` | `true` closes signups once you've created the accounts you need. Only the literal `true` counts. The server logs a startup warning while it is unset |
| `COOKIE_SECURE` | no | `false` | `true` marks the session cookie `Secure`. Requires TLS in front — a browser refuses a `Secure` cookie over plain HTTP, which breaks login with no error. The server logs a startup warning while it is unset |
| `TRUSTED_PROXIES` | no | — | Comma-separated addresses or CIDR blocks whose `X-Forwarded-For` / `X-Real-IP` are believed. Unset means neither header is trusted and the socket address wins — correct for a directly exposed server. Set it when running behind a reverse proxy, or every client shares one rate-limit bucket. See [Production](/docs/production#telling-skael-about-the-proxy) |
| `CORS_ORIGINS` | no | — | Comma-separated allowed origins, e.g. `https://app.example.com,http://localhost:5173`. Unset means no CORS headers are sent |
| `METRICS_ENABLED` | no | `true` | Set to `false` to drop the `/metrics` Prometheus endpoint and its instrumentation. Only the literal `false` disables it |
| `GITHUB_TOKEN` | no | — | GitHub token used by skill import; raises the GitHub API rate limit |

### Publish gate and evaluation

| Variable | Required | Default | Description |
|---|---|---|---|
| `QUALITY_FLOOR` | no | `0` | Minimum headline quality score (0–100) a verified evaluation must reach to release a version held for review. `0` accepts any verified report with a complete panel and no critical contract violations |

`QUALITY_FLOOR` is the only setting whose value the server validates. A value that isn't a number, or falls outside 0–100, stops the server from booting with an error naming the variable. Every other numeric and duration setting silently falls back to its default instead: `DB_MAX_CONNS=twenty` gives you 25 with no warning, `RATE_LIMIT_WRITE=` gives you 60, `LOG_LEVEL=verbose` gives you `info`. The floor is treated differently on purpose — a security control that reads as unset because of a typo looks configured and does nothing.

### Database pool

| Variable | Required | Default | Description |
|---|---|---|---|
| `DB_MAX_CONNS` | no | `25` | Maximum connections in the pool |
| `DB_MIN_CONNS` | no | `5` | Idle connections the pool keeps open |
| `DB_MAX_CONN_LIFETIME` | no | `1h` | Maximum lifetime of a connection before it is closed (Go duration) |
| `DB_MAX_CONN_IDLE_TIME` | no | `30m` | Maximum idle time before a connection is closed (Go duration) |
| `DB_HEALTH_CHECK_PERIOD` | no | `1m` | Interval between pool health checks (Go duration) |

### Rate limits

| Variable | Required | Default | Description |
|---|---|---|---|
| `RATE_LIMIT_AUTH` | no | `20` | Per-minute budget for `/api/auth/*` — login, signup, password reset |
| `RATE_LIMIT_EVENTS` | no | `600` | Per-minute budget for `POST /api/events` — activation tracking |
| `RATE_LIMIT_READ` | no | `300` | Per-minute budget for GET/HEAD routes — list, search, manifest, downloads |
| `RATE_LIMIT_WRITE` | no | `60` | Per-minute budget for every other mutating route — publish, import, delete |
| `RATE_LIMIT_SUITES` | no | `20` | Per-minute budget for `POST /api/eval/suites`, which accepts up to a 10MB archive per call |

`/api/auth/*` is keyed by source IP alone: those requests are unauthenticated, so an `X-API-Key` header on them is unverified and must not mint a fresh budget. Every other class is keyed by API key where one is present and by IP otherwise, and is additionally capped by a shared per-IP ceiling of ten times the class limit, checked first — so one source address cannot get more by rotating keys. Raising a class's limit raises its ceiling with it. Over-limit requests get a 429 with `Retry-After`, which the CLI honours.

### Scanning, logging and retention

| Variable | Required | Default | Description |
|---|---|---|---|
| `EXTERNAL_SCAN_CMD` | no | — | Opt-in external scanner run over each skill on publish and import. `{dir}` is replaced with the skill directory; the command must emit SARIF on stdout, e.g. `gitleaks dir {dir} --report-format sarif --report-path /dev/stdout`. Findings merge into the built-in scan |
| `EXTERNAL_SCAN_TIMEOUT` | no | `60s` | Per-scan timeout for `EXTERNAL_SCAN_CMD` (Go duration) |
| `EVENT_RETENTION_DAYS` | no | `90` | Days of activation events to keep. Older rows are purged once, at startup — not on a schedule. `0` disables the purge |
| `LOG_LEVEL` | no | `info` | `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic` |
| `LOG_FORMAT` | no | — | `pretty` for colorized console output. Anything else, including unset, logs JSON |
| `LOG_PRETTY` | no | `false` | `true` does the same as `LOG_FORMAT=pretty`; either one is enough |

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

This starts the platform plus a Postgres container with a persistent volume. The platform is at `http://localhost:8080`; sign up to create your first account. Publishing and scanning work right away. Evaluations do not — that's a separate opt-in piece, see below.

## Evaluation worker (optional)

The server queues evaluation jobs but never runs them — no Docker socket and no LLM key live on it. Running evaluations requires a separate `skael-worker` process, because it needs a Docker daemon to sandbox each run and a direct Anthropic API key to judge the result.

Run it on the host, alongside Compose rather than inside it:

```bash
export SKAEL_ENDPOINT=http://localhost:8080
export SKAEL_API_KEY=<a personal API key with permission to claim eval jobs>
export ANTHROPIC_API_KEY=<your direct Anthropic API key>
skael-worker
```

This is enough for a VPS: no interactive login step, no credential directory to provision. `ANTHROPIC_API_KEY` covers both the judge and the claude-code panel agent, since the worker forwards it into the sandbox as an environment variable.

**Why not a Compose service?** The worker bind-mounts its own working directory into each sandbox container through the Docker socket, and that mount is resolved by the host's Docker daemon. A worker running inside a container would hand the daemon a path that exists only in its own filesystem, so the mount would come up empty. Running it on the host keeps that path real.

### Worker configuration

These two tables are duplicated on [Quality scoring](/docs/quality#worker-environment-variables) — change both together.

Required — the worker exits at startup naming whichever is missing:

| Variable | Description |
|---|---|
| `SKAEL_ENDPOINT` | Base URL of the skael server the worker claims jobs from |
| `SKAEL_API_KEY` | API key the worker authenticates with |
| `ANTHROPIC_API_KEY` | Direct Anthropic API key for the judge model, and (forwarded into the sandbox) for the claude-code panel agent — never a subscription CLI on PATH |

Optional, with defaults:

| Variable | Default | Description |
|---|---|---|
| `CLAUDE_CODE_OAUTH_TOKEN` | — | Subscription auth for the claude-code panel agent, as an alternative to `ANTHROPIC_API_KEY`. Generate with `claude setup-token` |
| `WORKER_ID` | `{hostname}-{pid}` | Identifies this worker in job leases |
| `WORKER_LEASE` | `5m` | How long a claimed job's lease lasts before it's considered abandoned |
| `WORKER_POLL` | `15s` | Interval between claim attempts when the queue is empty |
| `WORKER_WORK_ROOT` | OS temp dir | Directory to materialise eval workspaces under |
| `WORKER_CONCURRENCY` | `1` | Must be a positive integer |
| `ANTHROPIC_AUTH_TOKEN` | — | Credential sent as `Authorization: Bearer` — what OpenRouter issues. An alternative to `ANTHROPIC_API_KEY`, and it wins when both are set |
| `ANTHROPIC_BASE_URL` | `https://api.anthropic.com` | An Anthropic-compatible gateway for the judge *and* the panel. Posts to `{base}/v1/messages` |
| `LLM_MODEL` | shipped defaults | Comma-separated model ids, most capable first. The first judges every run and leads the panel; later entries are the panel's floor members at the deep tier. Required behind a gateway that namespaces its identifiers |

The judge's credential is checked at startup — the worker exits naming the variables to set. The panel agent is not checked at startup: if no credential reaches the sandbox and no auth directory is mounted, the worker logs a warning naming the missing variables and the job comes back with an incomplete panel rather than an error. Only the claude-code adapter is wired up today. See [Quality scoring](/docs/quality) for the OpenRouter example and what changing the model means for score comparability.

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

| Variable | Default | Description |
|---|---|---|
| `STORAGE_PATH` | `./data/skills` | `s3://bucket/prefix` switches to S3; any other value is a local path |
| `S3_ENDPOINT` | `s3.amazonaws.com` | Set for MinIO, R2, Spaces. No `AWS_*` fallback |
| `S3_REGION` | `us-east-1` | Falls back to `AWS_REGION` when unset |
| `S3_ACCESS_KEY_ID` | — | Falls back to `AWS_ACCESS_KEY_ID` |
| `S3_SECRET_ACCESS_KEY` | — | Falls back to `AWS_SECRET_ACCESS_KEY` |
| `S3_USE_PATH_STYLE` | `false` | `true` for MinIO |
| `S3_USE_SSL` | `true` | `false` for local MinIO. Only the literal `false` disables it |

Credentials resolve in one step: if a key and a secret are both found — under either the `S3_*` or the `AWS_*` name — they are used as static credentials. If either is missing, skael falls back to an IAM instance role (EC2, ECS, EKS) and ignores the one that was set. So half a key pair is the same as none.

The bucket must already exist. skael checks it at startup and refuses to boot if it is missing or unreachable.
