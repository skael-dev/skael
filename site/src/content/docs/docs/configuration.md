---
title: Configuration
description: Every environment variable the server and the worker read.
---

## Server env vars

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `STORAGE_PATH` | no | `./data/skills` | Archive storage; local dir, or `s3://bucket/prefix` for S3 |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `DISABLE_SIGNUP` | no | `false` | Set to `true` (the literal string) to lock signups after setup. The server logs a startup warning when unset |
| `PUBLIC_URL` | with SSO, SMTP or GitHub | — | The URL people use to reach the server. Provider callbacks and email links are built from it |
| `SIGNUP_ALLOWED_DOMAINS` | no | — | Comma-separated email domains. Set it to refuse a new account on every sign-in path from any other domain |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | no | — | Sign-in through any OpenID Connect provider. The first sign-in creates a `member`, even with `DISABLE_SIGNUP=true`. Register `PUBLIC_URL/api/auth/oauth/oidc/callback` |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | no | — | Sign-in through Google |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | no | — | Sign-in through GitHub. `GITHUB_TOKEN` is separate and serves import only |
| `SKAEL_MASTER_KEYS` | for GitHub | — | Encrypts secrets stored from the UI, such as the GitHub App's private key. Comma-separated `id:base64key` pairs of 32 bytes each; the first seals, every one opens. Generate one with `openssl rand -base64 32`. Keep it: a new key makes stored secrets unreadable. To rotate, put the new key first and keep the old one after it |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` / `SMTP_FROM` | no | — / `587` / — / — / — | Turns on self-service password reset |
| `S3_ENDPOINT` / `S3_REGION` | no | `s3.amazonaws.com` / `us-east-1` | Only when `STORAGE_PATH=s3://…`. Only `S3_REGION` falls back to `AWS_REGION`; the endpoint has no `AWS_*` fallback |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | no | — | Both fall back to `AWS_*`. The pair is all-or-nothing: set only one and it is silently ignored. Omit both for an IAM instance role |
| `S3_USE_PATH_STYLE` / `S3_USE_SSL` | no | `false` / `true` | `S3_USE_PATH_STYLE=true` for MinIO |
| `EVENT_RETENTION_DAYS` | no | `90` | Days of skill_events to keep. The server purges older rows at startup and then daily while it runs. `0` disables cleanup |
| `WEBHOOK_ALLOW_PRIVATE` | no | `false` | Set to `true` (the literal string) to let webhook deliveries reach private addresses, loopback, shared address space and `http` URLs. Link-local addresses stay refused. See [Webhooks](/docs/webhooks#address-rule) |
| `EXTERNAL_SCAN_CMD` | no | — | Opt-in external scanner run on publish/import; `{dir}` → skill dir, must emit SARIF on stdout (e.g. `gitleaks dir {dir} --report-format sarif --report-path /dev/stdout`). Findings merge into the native scan. |
| `EXTERNAL_SCAN_TIMEOUT` | no | `60s` | Per-scan timeout for `EXTERNAL_SCAN_CMD` (Go duration) |
| `DB_MAX_CONNS` | no | `25` | Maximum number of connections in the pool |
| `DB_MIN_CONNS` | no | `5` | Minimum number of idle connections the pool maintains |
| `DB_MAX_CONN_LIFETIME` | no | `1h` | Maximum lifetime of a connection before it is closed (Go duration) |
| `DB_MAX_CONN_IDLE_TIME` | no | `30m` | Maximum idle time before a connection is closed (Go duration) |
| `DB_HEALTH_CHECK_PERIOD` | no | `1m` | Interval between pool health checks (Go duration) |
| `CORS_ORIGINS` | no | — | Comma-separated allowed origins for CORS (e.g. `https://app.skael.dev,http://localhost:5173`) |
| `TRUSTED_PROXIES` | no | — | Comma-separated CIDR blocks and/or bare addresses (IPv4 or IPv6) whose `X-Forwarded-For`, `X-Real-IP` and `X-Forwarded-Proto` may be believed, e.g. `10.0.0.0/8,192.168.1.5`. Empty (the default) ignores these headers and uses the socket address — correct for a directly-exposed server. Set it to the proxy's address when running behind one, or every client shares one rate-limit bucket |
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

Auth is via user accounts + personal API keys (no static server key). `DISABLE_SIGNUP=true` locks password, Google and GitHub signups after setup. OIDC stays open, because the identity provider is the gate.

## Worker env vars

`cmd/skael-worker` reads its own environment, independent of the server's `.env`:

| Variable | Required | Default | Description |
|---|---|---|---|
| `SKAEL_ENDPOINT` | yes | — | Base URL of the Skael server the worker claims jobs from |
| `SKAEL_API_KEY` | yes | — | API key the worker authenticates with |
| `ANTHROPIC_API_KEY` | one of the two | — | LLM credential, sent as `x-api-key`. The worker never falls back to a subscription CLI on PATH. Panel execution is separate: agent adapters with `AuthDirs` (e.g. claude-code) mount host credential directories into the sandbox and are subscription-backed wherever those exist |
| `ANTHROPIC_AUTH_TOKEN` | one of the two | — | LLM credential, sent as `Authorization: Bearer`. What OpenRouter and most compatible gateways issue. Wins over `ANTHROPIC_API_KEY` when both are set |
| `WORKER_ID` | no | `{hostname}-{pid}` | Identifies this worker in job leases |
| `WORKER_LEASE` | no | `5m` | How long a claimed job's lease lasts before it's considered abandoned (Go duration) |
| `WORKER_POLL` | no | `15s` | Interval between claim attempts when the queue is empty (Go duration) |
| `WORKER_WORK_ROOT` | no | `os.TempDir()` | Directory to materialise eval workspaces under. Never bind-mounted into a sandbox, so it does not need to be host-visible |
| `WORKER_RUN_ROOT` | only in a container | `os.TempDir()` | Directory per-session sandbox workspaces are created under. These **are** bind-mounted into sandbox containers, so the path must resolve identically for the worker and for the daemon starting them. Required when containerized — the worker refuses to start without it. The worker creates the directory at start if it does not exist. |
| `WORKER_CONCURRENCY` | no | `1` | Concurrent sandbox sessions. Must be a positive integer |
| `WORKER_GRADE_CONCURRENCY` | no | whetstone's default (8) | Concurrent judge calls. A separate resource from the sessions: a container is bounded by CPU and memory, a judge call by the account's rate limit. Must be a positive integer |
| `WORKER_CONTEST_PARALLEL` | no | `2` | Contest candidates run at once. Each candidate runs its own sessions, so this multiplies the sandbox load. Must be a positive integer |
| `ANTHROPIC_BASE_URL` | no | `https://api.anthropic.com` | An Anthropic-compatible gateway for **both** the judge (dialled here) and the panel (dialled inside the sandbox, forwarded by the claude-code adapter). It posts to `{base}/v1/messages`, so the base carries no `/v1` |
| `LLM_MODEL` | with a gateway | shipped defaults | Comma-separated model ids, most capable first. The first judges every run and leads the panel; later entries are the panel's floor members, which only the deep tier runs. Required behind a gateway that namespaces its identifiers |
| `CLAUDE_CODE_OAUTH_TOKEN` | no | — | Bills the *panel* to a Claude subscription (`claude setup-token`). The judge still needs one of the two credentials above. Set beside `ANTHROPIC_BASE_URL` it selects the split: the judge keeps the gateway, the panel does not see it |
| `SANDBOX_DRIVER` | no | `docker` | Selects the sandbox driver. `docker` starts sandbox containers on the local daemon; `kubernetes` schedules a pod through the cluster API instead; `northflank` runs each session as a service in a Northflank project. An unrecognised value fails startup naming it |
| `SANDBOX_K8S_NAMESPACE` | with `kubernetes` | — | Namespace the driver creates session pods in. This namespace must hold nothing but session pods: `pods/exec` is the driver's entire privilege surface, and whoever holds it can execute in any pod there |
| `SANDBOX_K8S_IMAGE` | no | `imagespec.PublishedBaseImage` (`ghcr.io/skael-dev/whetstone-base:<n>`) | Image a session pod runs. The driver resolves a published image and does not build one, so this must already be in a registry. The default is the image the release publishes, so an operator who wants the shipped environment does not name it. Set it to run your own base |
| `SANDBOX_K8S_PULL_SECRET` | no | — | Name of an image pull secret in `SANDBOX_K8S_NAMESPACE`, for a private registry |
| `SANDBOX_K8S_RUNTIME_CLASS` | no | — | `runtimeClassName` set on session pods, for example `kata`, for a hardware-isolated runtime |
| `SANDBOX_K8S_HARDWARE_ISOLATED` | no | unset | Asserts the runtime is hardware-isolated. Accepted only as the exact lowercase string `true`; any other value, including `TRUE` or `1`, reads as unasserted and the driver refuses runs that need isolation, same as the docker driver with none configured |
| `SANDBOX_K8S_NETWORK_POLICY` | no | unset | Asserts the cluster's CNI enforces `NetworkPolicy`. Accepted only as the exact lowercase string `true`; any other value, including `TRUE` or `1`, reads as unasserted and the driver refuses a run that restricts the network. Verify the assertion against the real cluster with `whetstone doctor --check-egress` before relying on it |
| `SANDBOX_NF_TOKEN` | with `northflank` | — | Northflank API token. Never printed by `whetstone doctor`, which reports only whether it is set |
| `SANDBOX_NF_PROJECT` | with `northflank` | — | Project the driver creates session services in. Give it a project of its own: the driver deletes every service carrying its owner label, including orphans found at startup |
| `SANDBOX_NF_IMAGE` | no | `imagespec.PublishedBaseImage` (`ghcr.io/skael-dev/whetstone-base:<n>`) | Base image a session runs |
| `SANDBOX_NF_REGISTRY_CREDENTIAL` | no | — | Saved Northflank credential name, for a private registry. Not needed for the default image |
| `SANDBOX_NF_PLAN` | no | `nf-compute-20` | Resource plan for each session service |
| `SANDBOX_NF_CLI` | no | `northflank` | Path to the CLI the driver shells out to for workspace transfer |
| `SANDBOX_NF_ALLOWED_DOMAINS` | for restricted runs | — | Comma-separated domains the operator's own project egress policy permits. Spaces around a comma are trimmed |
| `SANDBOX_NF_NETWORK_POLICY` | no | unset | Asserts that the domains above are actually enforced by the project's egress policy. Accepted only as the exact lowercase string `true`; any other value reads as unasserted and the driver refuses a restricted run |
| `SANDBOX_NF_HARDWARE_ISOLATED` | no | unset | Asserts the plan is hardware-isolated. Accepted only as the exact lowercase string `true`; any other value reads as unasserted and the driver refuses runs that need isolation, same as the docker driver with none configured |

The same four LLM variables configure `whetstone`, which additionally reads `WHETSTONE_LLM_TIMEOUT` and `WHETSTONE_BASE_TAG`. `whetstone doctor` prints what resolved.

Unlike the server, the worker's duration and integer parsing does **not** silently fall back — a malformed `WORKER_LEASE`, `WORKER_POLL`, `WORKER_CONCURRENCY` or `WORKER_CONTEST_PARALLEL` fails startup with the offending value named.

### Running the worker in a container

Supported, and how `docker compose --profile worker up` runs it (`Dockerfile.worker`). This section covers the **Docker driver** (`SANDBOX_DRIVER=docker`, the default). The worker starts sandbox containers as *siblings* through a mounted `/var/run/docker.sock` — not Docker-in-Docker. Two constraints follow, both load-bearing, and **both apply to the Docker driver only** — see below for why the Kubernetes driver needs neither:

- **`WORKER_RUN_ROOT` must be bind-mounted at the same path on both sides** (`-v /var/lib/skael/run:/var/lib/skael/run`). Session workspaces are bind-mounted into each sandbox and the *host* daemon resolves that path. A container-local path names nothing on the host, and Docker creates a missing bind source as an empty directory rather than failing — so the sandbox starts with no task and no skill and scores as a skill that did nothing. `requireHostSharedRoots` in `cmd/skael-worker` refuses to start containerized without it, because nothing downstream can distinguish that from a genuinely bad skill.
- **Auth must arrive as environment variables, not as a mounted credential directory.** `runner.resolveAuth` prefers an adapter's `AuthEnv` over its `AuthDirs` and returns *no* mounts when any of them is set, which is what removes the other class of host-path bind. Mounting `~/.claude` is the one auth style that stays host-only — subscription-backed panel execution still works containerized via `CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`), which is a token rather than a directory. `docker-compose.yml` carries all three supported setups (Anthropic key, subscription panel, OpenRouter) with two commented out.

### Running the worker against a Kubernetes cluster

`SANDBOX_DRIVER=kubernetes` schedules a session as a pod through the cluster API instead of talking to a Docker socket. This is the main practical reason the driver exists: a worker on a containerd node, or a worker that must not hold a Docker socket at all, has neither host-path constraint above to satisfy. `WORKER_RUN_ROOT` does not apply, because nothing is bind-mounted, and no node needs a Docker socket. Grant the worker's service account only `deploy/kubernetes/worker-rbac.yaml`, and put nothing but session pods in that namespace — `pods/exec` is the driver's entire privilege surface, and whoever holds it can execute in any pod there.

- **The Kubernetes driver resolves a published image; it does not build one.** `SANDBOX_K8S_IMAGE` names an image already in a registry, and defaults to `imagespec.PublishedBaseImage`, which `resolve.FromEnv` supplies. `Options.Validate` still requires a non-empty value, so a driver constructed directly rather than through `resolve` fails fast instead of pulling nothing. The default is `ghcr.io/skael-dev/whetstone-base:<n>`, where `<n>` is the version suffix of `imagespec.DefaultBaseTag`. The release workflow's `images` job publishes it from `imagespec.BaseDockerfile(false)` (`go run ./cmd/whetstone --print-base-dockerfile`), tagged by that suffix, never by the release version — the constant is bumped only when the base image's contents change, and tagging per release would publish a new environment every time and split every score trend. Bumping `DefaultBaseTag` requires a release before a Kubernetes worker can pull the new base; until then it keeps resolving the old tag.

Each of the events/read/write/suites classes also enforces a shared per-IP ceiling — `ipCeilingFactor` (10) × that class's limit — checked before the per-key budget, so one source address can't exceed it no matter how many distinct API keys it presents; raising the class's env var raises the ceiling proportionally.

