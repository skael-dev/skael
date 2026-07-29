---
title: Troubleshooting
description: Fixes for the most common skael problems.
---

## `skael setup` can't connect

**What you see:**
```
  ✗ Cannot connect to http://localhost:9

    http GET /api/health: Get "http://localhost:9/api/health": dial tcp [::1]:9: connect: connection refused

    Try: curl http://localhost:9/api/health
```

**Why:** The server isn't reachable — wrong URL, wrong port, or the process isn't running.

**Fix:**
1. Confirm the server is up: `curl http://your-host:8080/api/health` — you should get `{"status":"ok"}`.
2. Check `LISTEN_ADDR` in `.env` (default `:8080`). If you changed it, use the same port in `skael setup`.
3. Make sure the container or process is started (`docker compose up -d` or `systemctl start skael`).

---

## 401 / invalid API key

**What you see** (from `skael list` or any CLI command):
```
  ✗ API error 401: {"error":"unauthorized"}
```

**Why:** The API key in `~/.skael/config.json` is revoked, expired, or was never created for this server.

**Fix:**
1. Log in to the dashboard → **Settings → API keys** → create a new key.
2. Re-run setup with the new key: `skael setup <url> <new-key>`.
3. If you rotated the key manually, you can also edit `~/.skael/config.json` directly — the field is `api_key`.

---

## Sync runs but skills don't appear in the agent

**What you see:** `skael sync` completes without errors but the skill files aren't where you expect them.

**Why:** Scope mismatch. Sync places skills under either the **project root** (`.claude/skills/`, `.cursor/rules/`, etc. relative to the nearest `.git`) or the **user home** (`~/.claude/skills/`, etc.). The default is `project`. If you ran sync from a directory that isn't inside a git repo, the project root falls back to the current working directory.

**Fix:**

Run `skael doctor` to see the resolved path for each detected agent:
```
  ✓ claude-code: 3 skill(s) in ~/.claude/skills, hook installed
```
Add `--json` for scripting:
```bash
skael doctor --json | jq '.checks[] | {name, detail}'
```

To change scope permanently, re-run setup with `--scope user`:
```bash
skael setup <url> <key> --scope user
```

Or pass `--scope` per-sync:
```bash
skael sync --scope user
```

- **`project`** — skills land in `<git-root>/.claude/skills` (or agent equivalent). Shared when you commit `.claude/`.
- **`user`** — skills land in `~/.claude/skills`. Available in all projects.

---

## `checksum mismatch` during sync

**What you see** (from `skael sync`):
```
  ! checksum mismatch for my-skill (expected a1b2c3d4e5f60718, got 9f8e7d6c5b4a3210)
```

Source: `cli/sync.go` — the warning fires when the SHA-256 of the downloaded archive doesn't match the checksum recorded in the manifest.

**Why:** The archive file on the server doesn't match the checksum stored in the database. This happens after a partial restore — the database was restored to a newer state than the archive store, or vice versa.

**Fix:**
- Re-publish the affected skill: `skael publish <skill-dir>`. This uploads a fresh archive and updates the manifest checksum.
- If you're restoring from backup, make sure both the database dump and the archive store (`STORAGE_PATH`) are from the same snapshot. See the [backup and restore guide](/docs/backup-restore).

---

## Publish rejected: critical security findings

**What you see** (from `skael publish`):
```
  /tmp/my-skill/SKILL.md:9	critical    AWS access key ID detected
  /tmp/my-skill/SKILL.md:8	critical    AWS access key ID detected

  2 critical · 0 high · 0 medium · 0 info
  ✗ security findings block publish

    Try: fix the findings above, or ask an owner or admin to publish with --override
```

**Why:** The scanner found a secret (AWS key, token, private key, etc.) in the skill files. Both `critical` and `warn` (high severity) block publishing to protect your team.

**Fix:**
1. Run `skael scan <dir>` locally to see every finding with file and line numbers. Exit codes: `0` = clean, `1` = warn/high, `2` = critical.
2. Remove or replace the secret. Use environment variables or `~/.skael/config.json` patterns instead of hard-coded values.
3. Re-publish. If the finding is a deliberate example (documentation, test fixture) and you hold the `owner` or `admin` role, re-publish with `skael publish --override` — but only when you're certain the value is not a live credential. `--override` is recorded server-side. (`--skip-local-scan` only skips the client-side pre-check; the server scans again and still blocks on its own findings.)

---

## Database connection refused at startup

**What you see** in the server log:
```json
{"level":"fatal","error":"ping database: failed to connect to `user=skael database=skael`:\n\t[::1]:5432 (localhost): dial error: dial tcp [::1]:5432: connect: connection refused\n\t127.0.0.1:5432 (localhost): dial error: dial tcp 127.0.0.1:5432: connect: connection refused","time":"...","message":"database connection error"}
```

Source: `cmd/server/main.go` — `log.Fatal().Err(err).Msg("database connection error")`.

**Why:** The server can't reach Postgres. The most common causes are: Postgres hasn't started yet, `DATABASE_URL` points to the wrong host/port, or there's a firewall between the two.

**Fix:**
1. Confirm Postgres is running: `pg_isready -h localhost -p 5432` or `docker ps`.
2. Check `DATABASE_URL` in `.env` — host, port, user, password, and database name must all match.
3. If using Docker Compose, add a `depends_on` healthcheck so skael waits for Postgres to be ready before starting:
   ```yaml
   depends_on:
     db:
       condition: service_healthy
   ```
   See the [production deployment guide](/docs/production#docker-compose) for a full example.

---

## Dashboard login loop

**What you see:** You log in, get redirected back to the login page immediately — no error shown.

**Why:** `COOKIE_SECURE=true` is set but the server is serving plain HTTP (no TLS proxy in front). The browser refuses to store a `Secure` cookie over HTTP, so the session is never saved. Every request after login appears unauthenticated.

Source: `internal/server/server.go` — `sessionManager.Cookie.Secure = os.Getenv("COOKIE_SECURE") == "true"`.

**Fix:**
- **In production:** put a TLS-terminating reverse proxy (Caddy, nginx) in front of skael before enabling `COOKIE_SECURE=true`. See the [production deployment guide](/docs/production#reverse-proxy).
- **In development:** unset `COOKIE_SECURE` (or set it to `false`) in `.env`. The default is `false`, so this only happens if you explicitly set it.

---

## Hooks installed but no activations

**What you see:** `skael hook status` shows hooks installed, but the **Activations** tab in the dashboard stays empty.

**Why:** The hook script reads credentials from `~/.skael/config.json` at runtime — credentials are never embedded in the agent config file. If that config file is missing, moved, or has an empty `api_key` field, the hook exits silently (`exit 0`) without posting any events.

Source: `cli/hooks/script.go` — the hook script checks `[ ! -f "$CONFIG_FILE" ]` and exits if the file is absent.

**Fix:**
1. Check hook installation status:
   ```bash
   skael hook status
   ```
2. Confirm `~/.skael/config.json` exists and has a valid `endpoint` and `api_key`:
   ```bash
   cat ~/.skael/config.json
   ```
3. If the config is missing or stale, re-run setup to restore it:
   ```bash
   skael setup <url> <api-key>
   ```
4. Re-install hooks explicitly:
   ```bash
   skael hook install
   ```

After re-installing, trigger a skill invocation and check the Activations tab — events appear within a few seconds.
