---
title: Production deployment
description: Run skael behind TLS with a locked-down setup.
---

The [quickstart](/docs/quickstart) gets you running in minutes. Production adds five things: TLS termination, secure session cookies, encrypted Postgres connections, persistent storage, and locked-down signups. This page covers all five.

## Production checklist

| | What to change | Why |
|---|---|---|
| TLS | Put a reverse proxy in front | skael serves plain HTTP; TLS must be terminated upstream |
| `COOKIE_SECURE=true` | Uncomment in `.env` | Session cookie is refused over plain HTTP when set; login breaks without a proxy |
| `DATABASE_URL` with `sslmode=require` | Change from `sslmode=disable` | Encrypts traffic between skael and Postgres |
| Persistent volume or S3 | Mount a volume or set `STORAGE_PATH=s3://…` | Archives are lost on container restart without one |
| `DISABLE_SIGNUP=true` | Set after creating all accounts | Prevents anyone with network access from registering |

## Reverse proxy

### Caddy (recommended)

Caddy terminates TLS automatically via Let's Encrypt. The full config for a site is one block:

```
skael.example.com {
    reverse_proxy localhost:8080
}
```

Save this as `/etc/caddy/Caddyfile` and reload: `systemctl reload caddy`.

Caddy handles HTTPS certificate provisioning and renewal with no further configuration. If you need HTTP/2 or custom TLS settings, the [Caddy docs](https://caddyserver.com/docs/) cover those.

:::note[Syntax cross-check]
`caddy` was not available in this environment. The Caddyfile above was verified against the official Caddy v2 reverse proxy documentation. The `reverse_proxy` directive with a bare address is the canonical single-upstream form.
:::

### nginx

```nginx
server {
    listen 80;
    server_name skael.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name skael.example.com;

    ssl_certificate     /etc/ssl/certs/skael.example.com.crt;
    ssl_certificate_key /etc/ssl/private/skael.example.com.key;

    # skael caps request bodies at 10 MB server-side. The proxy limit must be
    # higher — nginx would reject the request first and return a 413 before
    # skael ever sees it. 12 MB gives a safe margin.
    client_max_body_size 12m;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
    }
}
```

:::note[Syntax cross-check]
`nginx` was not available in this environment. The block above was verified against the nginx `ngx_http_proxy_module` documentation. All directives are standard nginx 1.18+ syntax.
:::

## Environment

A production `.env` differs from the default in three places:

```ini
# Required — use your actual credentials; sslmode=require encrypts the
# connection between skael and Postgres.
DATABASE_URL=postgres://skael:strongpassword@db.example.com:5432/skael?sslmode=require

# Bind to loopback only. The reverse proxy reaches 127.0.0.1:8080;
# exposing 0.0.0.0 would make the plain-HTTP port reachable from outside.
LISTEN_ADDR=127.0.0.1:8080

# Tells the browser to send the session cookie only over HTTPS.
# Without this, the cookie travels in plaintext on any HTTP request.
# If you set this without a TLS proxy in front, login stops working.
COOKIE_SECURE=true

# Lock down signups after initial setup (see below).
DISABLE_SIGNUP=true

# Optional: use S3-compatible storage instead of a local volume.
# STORAGE_PATH=s3://my-bucket/skael
```

**Every variable above is read by the server.** Verified against `internal/platform/config.go` (`DATABASE_URL`, `STORAGE_PATH`, `LISTEN_ADDR`, `DISABLE_SIGNUP`) and `internal/server/server.go` (`COOKIE_SECURE`).

:::caution[COOKIE_SECURE and HTTP]
Setting `COOKIE_SECURE=true` without a TLS proxy causes login to silently fail. The browser refuses to send the cookie on plain HTTP, so every request after login appears unauthenticated. Set this only when you have TLS termination in front.
:::

## Storage

By default, skill archives go to `./data/skills` (or `STORAGE_PATH` if set). In Docker, that path disappears when the container is replaced.

**Local volume** — add a volume mount in docker-compose or `docker run`:

```yaml
volumes:
  - skill-data:/data/skills
environment:
  STORAGE_PATH: /data/skills
```

**S3-compatible object storage** — set `STORAGE_PATH=s3://bucket/prefix`. For full configuration options (endpoint, region, access keys, MinIO path-style), see the [self-hosting reference](/docs/self-hosting#object-storage-s3-compatible). The bucket must exist before starting skael.

## Locking down signups

Do this once, after all accounts are created.

1. Deploy with `DISABLE_SIGNUP` unset (or `false`). Sign up to create the owner account — the first account created on any instance automatically becomes the owner. See [Roles](#roles) below.
2. Log in to the dashboard. Go to **Settings → API keys** and create keys for each teammate who needs CLI access.
3. Distribute the API keys. Each person runs `skael setup <url> <key>` to connect their CLI.
4. Set `DISABLE_SIGNUP=true` in `.env` and restart the server.
5. Verify the lockdown: a POST to `/api/auth/signup` must return `403`.

```bash
curl -s -X POST https://skael.example.com/api/auth/signup \
  -H "Content-Type: application/json" \
  -d '{"email":"new@example.com","password":"somepassword","name":"New User"}'
```

Expected response when signups are disabled — HTTP 403:

```json
{"title":"Forbidden","status":403,"detail":"signup is disabled"}
```

If you see anything other than a 403, `DISABLE_SIGNUP=true` is not active. Check that the restart completed and the env var is set in the process environment, not only in `.env`.

## Roles

Every account has one of three roles:

| Role | Granted | Can do |
|---|---|---|
| `owner` | Automatically, to the first account created on the instance. Exactly one per instance, and it never changes hands via the API. | Everything, plus role management: `PUT /api/admin/users/{id}/role` (promote to `admin`/`member`), `GET /api/admin/users` (list all users), `POST /api/admin/reset-password` (issue another user a temporary password). |
| `admin` | By the owner, via the role-management endpoint above (or the dashboard). | Normal use, plus `skael publish --override` to publish a skill past a blocking scan finding. |
| `member` | Default for every new signup. | Normal use: publish, sync, browse. Cannot override a blocked publish. |

The owner cannot change their own role, and no one can be promoted to owner — an instance always has exactly one, set at account-creation time, so there is no path to a lockout.

:::caution[Upgrading from a pre-role build]
Before roles existed, every account but the first signed up with role `admin` by default. The upgrade migration leaves the first account's `owner` role untouched and downgrades every other existing `admin` account to `member` — a live behavior change on any multi-user instance. Log in as the owner afterward and re-promote anyone who needs `admin` (publish-override) access via `PUT /api/admin/users/{id}/role` or the dashboard — see [Upgrading](/docs/upgrading).
:::

## Health probes

skael exposes two endpoints:

| Endpoint | Purpose | Returns |
|---|---|---|
| `GET /api/health` | Liveness — always returns 200 if the process is running | `{"status":"ok"}` |
| `GET /api/health/ready` | Readiness — checks database and storage connectivity | `{"status":"ready","checks":{"database":"ok","storage":"ok"}}` or 503 |

Use liveness for restart decisions. Use readiness to gate traffic. Do not use the ready endpoint for liveness — a transient DB blip would restart the pod unnecessarily.

Actual responses from a running server:

```
GET /api/health → 200
{"status":"ok"}

GET /api/health/ready → 200
{"status":"ready","checks":{"database":"ok","storage":"ok"}}
```

When a dependency is unavailable, `/api/health/ready` returns HTTP 503. The response body describes which checks failed without exposing internal details (connection strings, hostnames, etc.).

### Kubernetes

```yaml
livenessProbe:
  httpGet:
    path: /api/health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /api/health/ready
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3
```

### Docker Compose

The skael image is built on `gcr.io/distroless/static-debian12` — no shell, no `curl`, no `wget`. Docker's `healthcheck` requires an executable inside the container, and distroless has none suitable.

The correct pattern is to run the health check from a separate container:

```yaml
services:
  server:
    image: ghcr.io/skael-dev/skael:latest
    ports:
      - "8080:8080"
    environment:
      DATABASE_URL: postgres://skael:pass@db:5432/skael?sslmode=require
      STORAGE_PATH: /data/skills
      COOKIE_SECURE: "true"
      DISABLE_SIGNUP: "true"
    volumes:
      - skill-data:/data/skills
    depends_on:
      db:
        condition: service_healthy

  healthcheck:
    image: curlimages/curl:latest
    entrypoint: /bin/sh
    command:
      - -c
      - |
        until curl -sf http://server:8080/api/health/ready; do
          sleep 2
        done
    depends_on:
      - server
    restart: "no"

  db:
    image: postgres:17
    environment:
      POSTGRES_USER: skael
      POSTGRES_PASSWORD: pass
      POSTGRES_DB: skael
    volumes:
      - pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U skael"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  skill-data:
  pg-data:
```

This gates startup sequencing only. Once the sidecar exits, Docker Compose does not monitor server health. For continuous monitoring use the Kubernetes `httpGet` probes above, or put monitoring at the orchestration layer (systemd, an external uptime check).

## Security scanning

Every publish and import runs skael's built-in scanner (a pure-Go package, no external dependencies, always on). It covers hardcoded secrets, prompt injection, data exfiltration, dangerous shell commands, and obfuscation, with a shell-AST pass that catches dangerous pipelines structurally. **Critical and high-severity findings block the publish**; an owner or admin can publish anyway with `skael publish --override`, which is recorded server-side. (`skael publish --force` is a deprecated alias for `--skip-local-scan` — it skips the client-side scan only, and does not bypass the server's own gate.)

### Optional external scanner

You can layer a second, free/open-source scanner on top via `EXTERNAL_SCAN_CMD`. skael runs it over each skill on publish/import, parses its SARIF output, and merges the findings (a SARIF `error` blocks; `warning`/`note` are advisory). It is opt-in and best-effort — if the tool is missing or errors, skael logs a warning and continues on the built-in scan alone.

```bash
# {dir} is replaced with the skill directory; the command must print SARIF to stdout.
EXTERNAL_SCAN_CMD=gitleaks dir {dir} --report-format sarif --report-path /dev/stdout --exit-code 0
EXTERNAL_SCAN_TIMEOUT=60s
```

Two free, offline options:

- **[gitleaks](https://github.com/gitleaks/gitleaks)** (MIT) — a single static Go binary; hardens secret detection with a frequently-updated ruleset. Secrets only.
- **[Cisco AI skill-scanner](https://github.com/cisco-ai-defense/skill-scanner)** (Apache-2.0) — purpose-built for `SKILL.md` + bundled scripts; matches skael's threat model most closely. `EXTERNAL_SCAN_CMD=skill-scanner scan {dir} --format sarif`. Run only its local analyzers — **do not** enable its optional LLM/VirusTotal/cloud features, which need paid third-party keys.

**Caveats (read before enabling):**

- The official `ghcr.io/skael-dev/skael` image is distroless — no shell, no `curl`, no Python. `EXTERNAL_SCAN_CMD` shells out, so the tool must exist in the container. Build a derived image that `COPY`s in the gitleaks static binary (easy), or for skill-scanner use a base image with Python 3.10+ (heavier). On bare-metal, just install the tool on the host.
- Each free general-purpose tool covers one layer: gitleaks = secrets, Semgrep = dangerous code in scripts, ClamAV/YARA = known-bad binaries. Only the purpose-built SKILL.md scanners (Cisco skill-scanner) cover the prompt-injection prose that is the core skill threat — and they carry a Python dependency. There is no single free, pure-Go, SKILL.md-aware external scanner.
- Snyk's "agent-scan" is **not** a fit: its real detection is a hosted API that needs a Snyk account/token and uploads your skill contents to a third party.
