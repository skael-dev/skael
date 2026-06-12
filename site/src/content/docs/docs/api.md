---
title: HTTP API
description: The skael server is API-first — everything the CLI and dashboard do goes through the HTTP API.
---

The CLI and dashboard are thin clients. Every operation — publishing skills, running sync, querying analytics — is an HTTP call to the same API you can call yourself.

## OpenAPI spec

The server generates and serves its own spec at:

```
GET /openapi.json
```

No auth required. The spec is generated from the live route definitions; it is always current.

```bash
curl http://localhost:8080/openapi.json
```

Response (truncated):

```json
{
  "info": { "title": "Skael API", "version": "1.0.0" },
  "paths": {
    "/api/skills": { "get": { "summary": "List skills" }, "post": { "summary": "Create a skill" } },
    ...
  }
}
```

There is no built-in browser UI (Swagger/Redoc) in this build. Import the spec into any compatible tool — [Insomnia](https://insomnia.rest), [Bruno](https://www.usebruno.com), or `curl`.

## Authentication

Two mechanisms are supported:

- **`X-API-Key` header** — for CLI, scripts, and automation. Create keys in the dashboard or via `POST /api/auth/keys`.
- **Session cookie** — for the dashboard only. The browser acquires it on login; you do not manage it manually.

Every endpoint except `/api/health`, `/api/health/ready`, and `/api/capabilities` requires authentication. The OpenAPI spec (`/openapi.json`) is not under `/api/` and requires no key.

```bash
curl http://localhost:8080/api/skills \
  -H "X-API-Key: sk-..."
```

Response:

```json
{
  "skills": [],
  "total": 0
}
```

An invalid or missing key returns `401 Unauthorized`.

## Endpoint overview

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/health` | Liveness probe — returns `{"status":"ok"}` unconditionally |
| `GET` | `/api/health/ready` | Readiness probe — verifies DB and storage connectivity |
| `GET` | `/api/capabilities` | Feature flags for this server edition |
| `GET` | `/openapi.json` | OpenAPI 3.1 spec |
| `POST` | `/api/auth/signup` | Create a user account — body requires `name`, `email`, `password` |
| `POST` | `/api/auth/login` | Log in (sets session cookie) |
| `POST` | `/api/auth/logout` | Destroy session |
| `GET` | `/api/auth/me` | Current user |
| `GET` | `/api/auth/keys` | List API keys |
| `POST` | `/api/auth/keys` | Create an API key |
| `DELETE` | `/api/auth/keys/{id}` | Delete an API key |
| `GET` | `/api/skills` | List skills (paginated) |
| `POST` | `/api/skills` | Create a skill |
| `GET` | `/api/skills/{name}` | Get a skill by name |
| `DELETE` | `/api/skills/{name}` | Delete a skill and its archives |
| `POST` | `/api/skills/register` | Register a skill stub (accepts display-style names; used by agent hooks) |
| `GET` | `/api/skills/{name}/versions` | List versions |
| `POST` | `/api/skills/{name}/versions` | Publish a new version (multipart binary, content-addressed) |
| `GET` | `/api/skills/{name}/versions/{version}/download` | Download version archive |
| `GET` | `/api/skills/{name}/scan` | Scan results for the latest version |
| `PUT` | `/api/skills/{name}/review` | Mark skill as reviewed |
| `DELETE` | `/api/skills/{name}/review` | Unmark skill as reviewed |
| `PUT` | `/api/skills/review` | Bulk mark skills as reviewed |
| `GET` | `/api/skills/tags` | Distinct tags across all skills |
| `GET` | `/api/skills/{name}/activations` | Activation summary for a skill |
| `GET` | `/api/skills/{name}/timeseries` | Per-agent daily activation counts |
| `GET` | `/api/skills/{name}/aliases` | List aliases pointing to this skill |
| `POST` | `/api/skills/{name}/aliases` | Create an alias |
| `DELETE` | `/api/skills/{name}/aliases/{alias}` | Delete an alias |
| `POST` | `/api/skills/merge` | Merge one skill into another |
| `POST` | `/api/events` | Ingest a skill activation event |
| `GET` | `/api/analytics/overview` | KPI totals |
| `GET` | `/api/analytics/skills` | Per-skill analytics (paginated) |
| `GET` | `/api/analytics/timeseries` | Daily activation counts for chart |
| `GET` | `/api/analytics/unregistered` | Skills seen in events but not in registry |
| `POST` | `/api/analytics/dismiss` | Dismiss an unregistered skill |
| `GET` | `/api/search` | Full-text + fuzzy skill search (`?q=...&limit=N`) |
| `GET` | `/api/sync/manifest` | Manifest used by `skael sync` (skill names + checksums) |
| `POST` | `/api/import/resolve` | Preview skills available for import from a URL |
| `POST` | `/api/import` | Import selected skills from a resolved source |
| `POST` | `/api/import/upload` | Import skills from a local archive upload |
| `GET` | `/api/import/sources` | List all imported skills with source provenance |
| `GET` | `/api/skills/{name}/source` | Source provenance for a single skill |

## Merge

`POST /api/skills/merge` collapses a source skill into a target skill.

What it does in one transaction:

1. All version records belonging to the source are reparented to the target. Version numbers are appended after the target's current latest (e.g. target has versions 1–3, source has versions 1–2 → they become versions 4 and 5 on target).
2. If the target has no import source, the source's import record is transferred to the target.
3. The source name is registered as an alias of the target — so tools that reference the old name will resolve to the new one via the alias table.
4. The source skill row is deleted.

The response is the target skill as it exists after the merge.

**Error cases:**
- `400` — source and target are the same name
- `404` — source or target does not exist

### Example

Create two skills and merge the old one into the canonical one:

```bash
# Create the source
curl -X POST http://localhost:8080/api/skills \
  -H "X-API-Key: sk-..." \
  -H "Content-Type: application/json" \
  -d '{"name":"old-deploy","description":"Legacy deployment helper"}'

# Create the target
curl -X POST http://localhost:8080/api/skills \
  -H "X-API-Key: sk-..." \
  -H "Content-Type: application/json" \
  -d '{"name":"deploy","description":"Deployment helper"}'

# Merge
curl -X POST http://localhost:8080/api/skills/merge \
  -H "X-API-Key: sk-..." \
  -H "Content-Type: application/json" \
  -d '{"source":"old-deploy","target":"deploy"}'
```

Response — the target skill:

```json
{
  "id": "51b3fe69-bf92-470d-93c9-5b3022da5d69",
  "name": "deploy",
  "description": "Deployment helper",
  "latest_version": 0,
  "frontmatter": {},
  "created_at": "2026-06-12T03:57:43.905778+02:00",
  "updated_at": "2026-06-12T03:57:43.905778+02:00"
}
```

After the merge, `GET /api/skills/old-deploy` returns `404`. `GET /api/skills/deploy/aliases` shows:

```json
[
  {
    "alias": "old-deploy",
    "canonical": "deploy",
    "created_at": "2026-06-12T03:57:47.48795+02:00"
  }
]
```

After the merge, `skael sync` tracks the canonical name automatically — the old row is gone. The alias is an audit record of the rename; the sync client resolves canonical names only.

## Aliases

Aliases are alternative names for a skill. They are stored in a separate `skill_aliases` table; the skills table itself is not modified.

**Resolution scope:** aliases are consulted by `Store.ResolveAlias` — which the sync manifest and merge path call directly. The `GET /api/skills/{name}` endpoint does **not** transparently resolve aliases; it looks up the canonical name only. If you request an alias name directly, you get `404`. Use `GET /api/skills/{canonical}/aliases` to discover what aliases exist, then dereference to the canonical name.

Constraint: creating an alias whose name matches an existing skill is rejected with `409 Conflict`.

### List aliases

```bash
curl http://localhost:8080/api/skills/deploy/aliases \
  -H "X-API-Key: sk-..."
```

Response:

```json
[
  {
    "alias": "old-deploy",
    "canonical": "deploy",
    "created_at": "2026-06-12T03:57:47.48795+02:00"
  }
]
```

### Create an alias

```bash
curl -X POST http://localhost:8080/api/skills/db-migrate/aliases \
  -H "X-API-Key: sk-..." \
  -H "Content-Type: application/json" \
  -d '{"alias":"migrate-db"}'
```

Returns `201 Created` on success. The target skill must exist; if it does not you get `404`.

### Delete an alias

```bash
curl -X DELETE http://localhost:8080/api/skills/db-migrate/aliases/migrate-db \
  -H "X-API-Key: sk-..."
```

Returns `204 No Content`. Returns `404` if the alias does not exist for that canonical skill.
