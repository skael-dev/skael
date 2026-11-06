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

- **`Authorization: Bearer` header** — for CLI, scripts, and automation. Create keys in the dashboard or via `POST /api/auth/keys`.
- **Session cookie** — for the dashboard only. The browser acquires it on login; you do not manage it manually.

Every endpoint except `/api/health`, `/api/health/ready`, `/api/capabilities`, and the account routes served by auth-all under `/api/auth` requires authentication. Those routes authenticate themselves. The OpenAPI spec (`/openapi.json`) is not under `/api/` and requires no key.

```bash
curl http://localhost:8080/api/skills \
  -H "Authorization: Bearer sk-..."
```

Response:

```json
{
  "skills": [],
  "total": 0
}
```

An invalid or missing key returns `401 Unauthorized`.

## Roles

Every account has one of three roles:

| Role | Who gets it |
|---|---|
| `owner` | The **instance owner** — the first account created, exactly one per instance, set automatically on first signup. |
| `admin` | Granted by the instance owner via `PUT /api/admin/users/{id}/role`. |
| `member` | The default for every new signup. |

Two terms are used throughout this page, because "owner" alone is ambiguous once [ownership rules](#ownership) exist:

- **instance admin** — an account whose role is `owner` or `admin`. This is the check most privileged routes make.
- **instance owner** — specifically the `owner` account. Only the `/api/admin/*` routes require it, apart from River UI.
- A **skill owner** or **namespace owner** is a member of an ownership rule. That has nothing to do with role — a plain `member` is usually the namespace owner for their team. See [Ownership](#ownership).

Role gates these, and nothing else:

- `/api/admin/*` — instance owner only. Listing users, changing a role, resetting another user's password. River UI at `/api/admin/river` is the one exception: an instance admin also reaches it.
- Publishing past an appealable security finding with `?override=true` (or `skael publish --override`) — instance admin. Credential-theft and exfiltration findings are unappealable and no role overrides them.
- Clearing the `scan` hold reason on a version held for review — instance admin. The `ownership` hold reason is different; see [Review queue](#review-queue).
- The eval queue's privileged operations: `POST /api/eval/jobs/claim`, `POST /api/eval/jobs/{id}/cancel`, and `POST /api/skills/{name}/evals` — instance admin.
- Reading the full quality report of a version that is still held for review — instance admin. See [Quality scores](#quality-scores).

Everything else — publish, sync, browse, search, reading who owns what — is open to any authenticated account.

## Rate limiting

Requests are throttled per route class, each with its own per-minute budget: auth (`/api/auth/*`, default 20), event ingestion (`POST /api/events` and `POST /api/events/batch`, default 600), reads (GET/HEAD, default 300), and writes (everything else, default 60). Operators can override each via `RATE_LIMIT_AUTH` / `RATE_LIMIT_EVENTS` / `RATE_LIMIT_READ` / `RATE_LIMIT_WRITE`. Requests carrying a key are budgeted per key; unauthenticated or keyless requests are budgeted per source IP (auth requests are always budgeted by IP, since the key on a login/signup call is unverified).

"Source IP" means the address the connection came from, not whatever `X-Forwarded-For` claims — those headers are read only from a peer listed in `TRUSTED_PROXIES`. Behind a reverse proxy, set that variable or every client is budgeted as one; see [Production](/docs/production#telling-skael-about-the-proxy).

The events/read/write classes also enforce a shared ceiling per source IP — ten times that class's limit — checked before the per-key budget, so a whole team behind one office IP can't collectively exceed it no matter how many distinct API keys they present between them; raising the class's env var raises this ceiling proportionally. If requests fail with the per-key budget apparently unused, this shared IP ceiling is almost always why.

A request over budget gets `429 Too Many Requests` with a `Retry-After` header, which the CLI honours automatically.

## Endpoint overview

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/health` | Liveness probe — returns `{"status":"ok"}` unconditionally |
| `GET` | `/api/health/ready` | Readiness probe — verifies DB and storage connectivity |
| `GET` | `/api/capabilities` | Feature flags for this server edition |
| `GET` | `/openapi.json` | OpenAPI 3.1 spec |
| `POST` | `/api/auth/sign-up/email` | Create a user account — body requires `name`, `email`, `password` |
| `POST` | `/api/auth/sign-in/email` | Sign in (sets session cookie) |
| `POST` | `/api/auth/sign-out` | End the session |
| `GET` | `/api/auth/session` | Current user and session |
| `GET` | `/api/auth/account` | Password change state, linked providers and TOTP state |
| `GET` | `/api/auth/keys` | List API keys |
| `POST` | `/api/auth/keys` | Create an API key |
| `DELETE` | `/api/auth/keys/{id}` | Delete an API key |
| `POST` | `/api/auth/password/change` | Change your own password — body requires `currentPassword` and `newPassword` |
| `GET` | `/api/users/search` | Find users by name or email (`?q=...`). Any authenticated account; returns `{id, name, email}` only, minimum 2 characters, at most 20 results |
| `POST` | `/api/admin/users/{id}/password` | Issue a temporary password (owner) |
| `POST` | `/api/admin/users/{id}/disable` | Disable a user and end their sessions (owner) |
| `POST` | `/api/admin/users/{id}/enable` | Enable a user (owner) |
| `GET` | `/api/admin/users` | List all users (instance owner only) |
| `PUT` | `/api/admin/users/{id}/role` | Set a user's role to `admin` or `member` (instance owner only) — see [Roles](#roles) |
| `GET` | `/api/skills` | List skills (paginated) |
| `POST` | `/api/skills` | Create a skill |
| `GET` | `/api/skills/{name}` | Get a skill by name |
| `DELETE` | `/api/skills/{name}` | Delete a skill and its archives |
| `POST` | `/api/skills/register` | Register a skill stub (accepts display-style names; used by agent hooks) |
| `GET` | `/api/skills/{name}/versions` | List versions |
| `POST` | `/api/skills/{name}/versions` | Publish a new version (multipart binary, content-addressed). `?override=true` publishes despite appealable scan findings — instance admin only, recorded server-side |
| `GET` | `/api/skills/{name}/versions/{version}/download` | Download version archive |
| `GET` | `/api/skills/{name}/versions/{version}/files/{path}` | Download one file from the version archive, as `application/octet-stream` with its size in `X-Skael-File-Size`. The read rule is the same as the archive download: a scoped key that is not a worker gets 404 on a held version. A path that climbs out of the archive gets 400 |
| `GET` | `/api/skills/{name}/versions/{version}/diff` | What this version changes against the version currently served — SKILL.md as a unified diff, plus per-file added/modified/removed |
| `GET` | `/api/skills/{name}/scan` | Scan results for the latest version |
| `PUT` | `/api/skills/{name}/review` | Mark skill as reviewed |
| `DELETE` | `/api/skills/{name}/review` | Unmark skill as reviewed |
| `PUT` | `/api/skills/review` | Bulk mark skills as reviewed |
| `GET` | `/api/skills/tags` | Distinct tags across all skills |
| `GET` | `/api/skills/{name}/activations` | Activation summary for a skill, with `hook_activations`, `automated_activations` and `by_consumer`. Takes `consumer` and `role`, described below the table |
| `GET` | `/api/skills/{name}/timeseries` | Per-agent daily activation counts. Takes `consumer` and `role`, described below the table |
| `GET` | `/api/skills/{name}/aliases` | List aliases pointing to this skill |
| `POST` | `/api/skills/{name}/aliases` | Create an alias |
| `DELETE` | `/api/skills/{name}/aliases/{alias}` | Delete an alias |
| `POST` | `/api/skills/merge` | Merge one skill into another |
| `GET` | `/api/ownership/rules` | List every [ownership rule](#ownership) and its members — any authenticated account |
| `POST` | `/api/ownership/rules` | Create a rule, or replace the members of the rule already at that pattern (upsert keyed on `pattern`) |
| `PUT` | `/api/ownership/rules/{id}` | Replace one rule's members. The pattern is not editable |
| `DELETE` | `/api/ownership/rules/{id}` | Delete a rule. Returns `{"deleted": true}` |
| `GET` | `/api/skills/{name}/owners` | Who owns this name, and which rule pattern produced them |
| `POST` | `/api/events` | Ingest a skill activation event |
| `GET` | `/api/analytics/overview` | KPI totals. Takes `consumer` and `role`, described below the table. They change the activation counts, not the skill, review or security counts |
| `GET` | `/api/analytics/skills` | Per-skill analytics (paginated). Takes `consumer` and `role`, described below the table. With a filter, the list holds only skills with a matching activation |
| `GET` | `/api/analytics/timeseries` | Daily activation counts for chart. Takes `consumer` and `role`, described below the table |
| `GET` | `/api/analytics/consumers` | Distinct consumers and roles in stored events, as `{"consumers": [], "roles": []}` |
| `GET` | `/api/analytics/unregistered` | Skills seen in events but not in registry |
| `POST` | `/api/analytics/dismiss` | Dismiss an unregistered skill |
| `GET` | `/api/search` | Full-text + fuzzy skill search (`?q=...&limit=N`) |
| `GET` | `/api/sync/manifest` | Manifest used by `skael sync` (skill names + checksums) |
| `POST` | `/api/import/resolve` | Preview skills available for import from a URL |
| `POST` | `/api/import` | Import selected skills from a resolved source |
| `POST` | `/api/import/upload` | Import skills from a local archive upload |
| `GET` | `/api/import/sources` | List all imported skills with source provenance |
| `GET` | `/api/skills/{name}/source` | Source provenance for a single skill |
| `GET` | `/api/skills/{name}/quality` | Most recent [quality score](/docs/quality) for a skill, across all its versions |
| `GET` | `/api/skills/{name}/quality/{version}` | One version's quality score, plus its full report |
| `GET` | `/api/skills/{name}/quality/history` | Every score for a skill, newest first, ungrouped |
| `GET` | `/api/skills/{name}/quality/series` | Quality history grouped into comparable runs |
| `GET` | `/api/skills/{name}/evals` | Evaluation jobs for a skill, newest first |
| `POST` | `/api/skills/{name}/evals` | Enqueue an evaluation against a different model panel — instance admin only |
| `POST` | `/api/eval/suites` | Upload an evaluation suite (base64 archive; the pusher's own check results are optional). Stored content-addressably; returns its `ref` |
| `GET` | `/api/eval/suites/{ref}/meta` | The spec and origin recorded for a suite |
| `GET` | `/api/eval/suites/{ref}` | Download the suite archive (`application/gzip`) |
| `GET` | `/api/review/queue` | Every version currently held for review, across all skills |
| `POST` | `/api/skills/{name}/versions/{version}/review` | Approve or reject one hold reason on a version held for review — see [Review queue](#review-queue) |
| `GET` | `/api/sets` | List sets. A set-limited key sees its own set. See [Sets](#sets) |
| `POST` | `/api/sets` | Create a set. Owner or admin only |
| `GET` | `/api/sets/{name}` | One set, with its entries and last callers |
| `PUT` | `/api/sets/{name}` | Replace a set's description and entries. Owner or admin only |
| `DELETE` | `/api/sets/{name}` | Delete a set. Owner or admin only |
| `GET` | `/api/sets/{name}/resolve` | Resolve a set to exact versions and digests. `?include_excluded=true` lists excluded entries |
| `GET` | `/api/sets/{name}/resolutions/{id}` | One stored resolution |
| `GET` | `/api/sets/{name}/bundle` | Download one resolution as a `tar.gz`. `?resolved=<id>` selects it |
| `GET` | `/api/admin/github` | Whether the instance has a GitHub App and a master key. Owner or admin only. See [Git sync](/docs/git-sync) |
| `POST` | `/api/admin/github/manifest` | Start creating the GitHub App. Returns the GitHub URL and the manifest to post there. Owner or admin only |
| `GET` | `/api/admin/github/callback` | GitHub returns here with the code. Redirects to the GitHub page |
| `DELETE` | `/api/admin/github` | Forget the GitHub App. Owner or admin only |
| `POST` | `/api/github/webhook` | GitHub's events, signed with the App's webhook secret. A push brings a sync forward |
| `GET` | `/api/git/repos` | The repos the App's installations reach |
| `POST` | `/api/git/connections/preview` | What a connection would adopt, create and refuse at the branch head |
| `POST` | `/api/git/connections` | Create a connection. `adopt` names the existing skills to bind. Admin or an owner of the prefix |
| `GET` | `/api/git/connections` | List connections |
| `GET` | `/api/git/connections/{id}` | A connection with its skills and the folders the last sync refused |
| `PUT` | `/api/git/connections/{id}` | Change `contest_mode`: `off`, `on_request` or `ready` |
| `DELETE` | `/api/git/connections/{id}` | Delete a connection. Its skills become native |
| `POST` | `/api/git/connections/{id}/sync` | Sync now |
| `POST` | `/api/git/connections/{id}/adopt` | Bind a skill the last sync refused as taken |
| `GET` | `/api/git/connections/{id}/pulls` | The Skael check on each open pull request, with each PR head and its contest |
| `POST` | `/api/git/connections/{id}/pulls/{number}/contest` | Ask for a contest on a pull request's newest head |
| `GET` | `/api/git/pr-heads/{id}/archive` | A stored PR head. A worker key with `eval:work`, or an owner or admin |
| `GET` | `/api/skills/{name}/git` | Where a git-bound skill lives. `bound` is false for a native skill |
| `GET` | `/api/skills/{name}/changes` | The pull requests Skael opened for a git-bound skill |
| `POST` | `/api/skills/{name}/detach` | Make a git-bound skill native |
| `GET` | `/api/service-accounts` | List service accounts. Owner or admin only. See [Service accounts](#service-accounts) |
| `POST` | `/api/service-accounts` | Create a service account. Owner or admin only |
| `DELETE` | `/api/service-accounts/{id}` | Delete a service account and revoke its keys. Owner or admin only |
| `GET` | `/api/service-accounts/{id}/keys` | List an account's keys. Owner or admin only |
| `POST` | `/api/service-accounts/{id}/keys` | Create a scoped key. Owner or admin only. The response has the secret once |
| `DELETE` | `/api/service-accounts/{id}/keys/{keyId}` | Revoke a key. Owner or admin only |
| `POST` | `/api/events/batch` | Report the activations of automated runs. See [Run signals](#run-signals) |
| `POST` | `/api/outcomes` | Report how an automated run ended |
| `GET` | `/api/skills/{name}/outcomes` | Outcomes of the runs that loaded each version of a skill |
| `POST` | `/api/submissions` | Submit a skill archive for human review. See [Submissions](#submissions) |
| `GET` | `/api/submissions` | List submissions. People only |
| `GET` | `/api/submissions/{id}` | One submission, its state and its reason |
| `GET` | `/api/submissions/{id}/diff` | Diff a submission against the served version. People only |
| `POST` | `/api/submissions/{id}/decision` | Approve or reject a submission. People only |
| `GET` | `/api/webhooks/endpoints` | List webhook endpoints. Owner or admin only. See [Webhooks](#webhooks) |
| `POST` | `/api/webhooks/endpoints` | Register an endpoint. Owner or admin only. The response has the secret once |
| `GET` | `/api/webhooks/endpoints/{id}` | One endpoint. Owner or admin only |
| `PUT` | `/api/webhooks/endpoints/{id}` | Replace an endpoint's settings. Owner or admin only |
| `DELETE` | `/api/webhooks/endpoints/{id}` | Delete an endpoint and its log. Owner or admin only |
| `POST` | `/api/webhooks/endpoints/{id}/rotate-secret` | Replace the secret. Owner or admin only. The response has the secret once |
| `POST` | `/api/webhooks/endpoints/{id}/test` | Send a test event. Owner or admin only |
| `GET` | `/api/webhooks/endpoints/{id}/deliveries` | List the delivery log. Owner or admin only |
| `POST` | `/api/webhooks/deliveries/{id}/redeliver` | Send a delivery again with the same event id. Owner or admin only |
| `GET` | `/api/admin/river` | River UI, the background job inspector. Owner or admin only |

The `consumer` and `role` query parameters narrow activation counts to the events one [service account](#service-accounts) reported, and to one role. Each is optional, and an omitted parameter applies no filter. A filter excludes hook events, because hook events have no consumer and no role.

### Worker-facing endpoints

These are the claim/lease protocol `skael-worker` speaks. They are an integration surface, not something you call by hand — a claim hands out a `X-Claim-Token` that every subsequent call on that job must present, and a job with a lapsed lease returns to the pool. Documented so you can build your own worker or debug a stuck queue.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/eval/jobs/claim` | Claim the next queued job. `204` when the queue is empty. Instance admin only. Returns the job and its `claim_token` |
| `POST` | `/api/eval/jobs/{id}/heartbeat` | Extend the lease on a claimed job. `409` once the lease is lost, `403` on a bad token |
| `POST` | `/api/eval/jobs/{id}/report` | Post the finished eval report and complete the job. The skill and version come from the job row, never the report body |
| `POST` | `/api/eval/jobs/{id}/fail` | Mark a claimed job failed, with an optional `error` string |
| `POST` | `/api/eval/jobs/{id}/cancel` | Cancel a job that has not finished — instance admin only, no claim token |
| `GET` | `/api/eval/jobs/{id}` | Job status, plus `queue_position` while it is still queued |

## Sets

A set is a name, a description and a list of entries. An entry names a skill. It has a `version` to pin one version, and no `version` to follow the latest.

```json
{"name": "review-tools", "description": "Skills for review", "entries": [{"name": "lint"}, {"name": "audit", "version": 3}]}
```

### Resolve

`GET /api/sets/{name}/resolve` returns the exact versions:

```json
{
  "id": "9f2c...",
  "set": "review-tools",
  "complete": false,
  "entries": [
    {"name": "lint", "version": 7, "sha256": "ab12...", "scan_verdict": "clean", "approval_state": "released"}
  ],
  "excluded": [
    {"name": "audit", "requested": "3", "reason": "held"}
  ]
}
```

- A set with an excluded entry returns 200 and `complete: false`. `excluded` is present only with `?include_excluded=true`.
- The reasons are `skill_not_found`, `no_released_version`, `version_not_found`, `held` and `rejected`.
- The `id` is the SHA-256 of the sorted name, version and digest list. Two resolves with no publish between them return the same `id`.
- A resolution does not change and does not expire.

### Bundle

`GET /api/sets/{name}/bundle?resolved=<id>` returns exactly the versions of that resolution. With no `resolved`, the server resolves the set now. Both forms return the resolution id in the `X-Skael-Resolution` header, and `true` or `false` in `X-Skael-Complete`.

The archive holds:

| Path | Content |
|---|---|
| `.agents/skills/manifest.json` | The set, the resolution id, and the name, version, `sha256` and `archive` of each skill |
| `.agents/skills/.archives/<name>@<version>.tar.gz` | The skill's archive, whose SHA-256 equals `sha256` |
| `.agents/skills/<name>/` | The unpacked skill |

Check the digest on the archive. An unpacked tree cannot reproduce it. `skael pull` does this for you.

A resolution that names a deleted skill returns 410 and names the skill.

## Service accounts

A service account is a machine identity. It has no password, it cannot sign in, and it never appears in user search. It acts only through its keys.

`POST /api/service-accounts/{id}/keys` takes a name, at least one scope and an optional set id:

```json
{"name": "ci", "scopes": ["sets:read"], "set_id": "c7a1..."}
```

| Scope | What it reaches |
|---|---|
| `skills:read` | Read routes for skills, search and the sync manifest |
| `sets:read` | Read routes for sets, resolutions and bundles |
| `eval:work` | The worker's claim, heartbeat, report and fail routes, and skill and suite reads a worker needs |
| `events:write` | `POST /api/events/batch` |
| `outcomes:write` | `POST /api/outcomes`, and `report_outcome` on `/mcp` |
| `submissions:write` | `POST /api/submissions` and `GET /api/submissions/{id}`, and `submit_skill` and `get_submission` on `/mcp` |

A key with scopes is denied by default: a route with no matching scope returns 403. Admin, user, key, ownership, review and publish routes have no scope, so a service key never reaches them.

A key with a `set_id` reaches only that set, and it needs `sets:read`. It gets 403 on `/api/skills` and on skill downloads. If the set is deleted, the key gets 403. It never becomes unrestricted.

## Run signals

A consumer reports what each automated run loaded and how the run ended. The consumer comes from the service account, and no request field can name one, so only a service account key can report. A personal key gets 403. The first event or outcome that names an external run id creates the run.

Outcomes never change the headline and never release a version. They come from uncontrolled tasks, and a consumer reports them about itself.

### Report events

`POST /api/events/batch` needs `events:write`. It holds from 1 to 1,000 events:

```json
{"events": [{"skill": "lint", "version": 3, "agent": "claude-code", "role": "implementer", "external_run_id": "run-81", "time": "2026-11-02T10:00:00Z", "set": "review-tools", "resolution_id": "9f2c..."}]}
```

`set` and `resolution_id` are optional. The response is `{"stored": 1, "runs": 1}`.

Send an `Idempotency-Key` header to make a retry safe. A repeat of a key returns the first result and stores nothing.

A batch is all or nothing. An invalid event stores nothing and returns 422 with its zero-based index:

```json
{"status": 422, "title": "Unprocessable Entity", "detail": "event 2: role is required", "index": 2}
```

A value with the wrong type or an unknown field fails the request schema. That 422 has no `index` field. It names the event in `errors[].location`, such as `body.events[2].time`.

### Report an outcome

`POST /api/outcomes` needs `outcomes:write`:

```json
{"external_run_id": "run-81", "outcome": "failed", "repairs": 2, "review_verdict": "changes requested", "evidence_url": "https://ci.example/81"}
```

`outcome` is `succeeded`, `failed` or `cancelled`. `evidence_url` must be an http or https URL. The body also takes `set` and `resolution_id`. A second outcome for one run replaces the first, and the audit log keeps the old value.

### Read outcomes

`GET /api/skills/{name}/outcomes` needs `skills:read`. It returns `min_comparable_runs` and, for each consumer, each version that a run loaded: the counts, `success_rate`, `mean_repairs` and `sample_too_small`. The rates are null below 20 runs that succeeded or failed. `comparison` is present only when the version and the previous released version both have 20 such runs. It states a difference and no cause.

## Submissions

A submission is a whole skill archive that an agent sends for human review. It has no version until a person approves it, so it is in no resolve, sync or download.

`POST /api/submissions` needs `submissions:write`:

```json
{"name": "lint", "rationale": "Handles the new config", "run_link": "https://ci.example/81", "external_run_id": "run-81", "archive_base64": "H4sI..."}
```

The scan runs at submission. An exfiltration or secret finding returns 422 and stores nothing. Other findings go to the reviewer.

`GET /api/submissions/{id}` returns the `state`, which is `pending`, `approved` or `rejected`, and the `reason`. A key reads only the submissions of its own consumer. Another submission returns 404.

`GET /api/submissions` takes `?state=` and `?limit=`. `GET /api/submissions/{id}/diff` compares a submission with the served version. No scope reaches these two routes.

`POST /api/submissions/{id}/decision` takes `{"action": "reject", "reason": "..."}` or `{"action": "approve"}`, and `edits` to change text before approval. No scope reaches it. An owner of the matching ownership rule, or an instance owner or admin, decides. A rejection needs a reason. Approval publishes through the normal gate as that person, so a blocking finding still holds the version. A second decision returns 409.

## Webhooks

An instance admin registers an endpoint, and the server sends it signed events. [Webhooks](/docs/webhooks) gives the event types, the payloads, the signature rule and the address rule.

`POST /api/webhooks/endpoints` takes this body:

```json
{"name": "ci-cache", "url": "https://ci.example.com/skael", "event_types": ["set.changed"], "set_ids": ["c7a1..."]}
```

- `event_types` needs at least one of `skill.version.published`, `skill.version.approved`, `set.changed` and `submission.decided`.
- `set_ids` limits `set.changed` to those sets. Empty means all sets.
- A refused address, an unknown event type or an unknown set id returns 422.

`PUT /api/webhooks/endpoints/{id}` replaces the same fields. It also takes `enabled`, and `enabled` stays as it is when you omit it.

`POST /api/webhooks/endpoints/{id}/rotate-secret` takes `{"window_hours": 48}`. The old secret stays valid for that window. The range is 0 to 168, and the default is 24.

`GET /api/webhooks/endpoints/{id}/deliveries` takes `limit`, from 1 to 200, and the default is 50. Each delivery has its event id, its state and every attempt. The log holds no response body.

The test and redeliver routes return 202 with no body. A service key never reaches these routes.

## River UI

River UI lives at `/api/admin/river`, for instance owners and admins. It shows the server's background jobs, and a job's arguments hold only ids. A member and a service key get 403.

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
  -H "Authorization: Bearer sk-..." \
  -H "Content-Type: application/json" \
  -d '{"name":"old-deploy","description":"Legacy deployment helper"}'

# Create the target
curl -X POST http://localhost:8080/api/skills \
  -H "Authorization: Bearer sk-..." \
  -H "Content-Type: application/json" \
  -d '{"name":"deploy","description":"Deployment helper"}'

# Merge
curl -X POST http://localhost:8080/api/skills/merge \
  -H "Authorization: Bearer sk-..." \
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
  -H "Authorization: Bearer sk-..."
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
  -H "Authorization: Bearer sk-..." \
  -H "Content-Type: application/json" \
  -d '{"alias":"migrate-db"}'
```

Returns `201 Created` on success. The target skill must exist; if it does not you get `404`.

### Delete an alias

```bash
curl -X DELETE http://localhost:8080/api/skills/db-migrate/aliases/migrate-db \
  -H "Authorization: Bearer sk-..."
```

Returns `204 No Content`. Returns `404` if the alias does not exist for that canonical skill.

## Ownership

See [Skill ownership](/docs/ownership) for why this exists and how to roll it out. This section is the wire contract.

An ownership rule answers one question: who reviews changes to this skill name. A rule is a **pattern** plus **at least one member**. A rule with no members is rejected with `422` — it would resolve a name to "owned" with nobody able to review it, which is strictly worse than leaving the name unowned, where instance admins still answer.

### Patterns

Exactly three shapes, and no others:

| Shape | Example | Matches |
|---|---|---|
| Exact name | `deploy` | only `deploy` |
| Namespace prefix | `payments:*` | every name starting `payments:` |
| Everything | `*` | every name |

`*` may only be the final character. `pay*ments` and `*payments` are rejected with `422`, as is any pattern with more than one `*`. There are no character classes and no mid-string globs — a grammar that fits in your head is a grammar people trust. Strip the trailing `*` (and one trailing `:`) and what remains must be a valid skill name.

Two things the grammar permits that the shorthand "namespace prefix" hides:

- The `:` is not required. `pay*` is a legal prefix and matches `payments` and `paypal` alike. Prefer the namespaced form; plain string prefixes catch names you did not mean to claim.
- `payments:*` does **not** match the bare name `payments`. The scope is literally `payments:`, and `payments` does not start with that. Write a second exact rule if you want both.

### Resolution

One rule governs a name. Exact match beats the longest matching prefix, which beats unowned.

**Longest match replaces, it does not stack.** This is the CODEOWNERS model. If `payments:*` is owned by the payments team and `payments:refunds` is owned by one person, then the payments team does *not* own `payments:refunds` — the more specific rule wins outright. That is what makes delegation possible: without replacement, a namespace owner could never hand a skill away.

The exception is management rights, not resolution. A member of a strictly containing rule may still create, edit, or delete the narrower rule, so the payments team can take `payments:refunds` back. Rights only ever narrow — no rule you belong to lets you touch a pattern that contains it.

Two more things worth knowing:

- Unowned does not hold a publish. Only a *matched* rule does. Protection switches on per namespace when someone writes the first rule covering it, so an instance with no rules holds nothing for ownership.
- Publishing a **first** version of a name that resolves to unowned claims it: the publisher becomes its sole skill owner via an exact-pattern rule. If a rule already covers the name, the rule wins and the publish is held for review like any other proposal.

### Reading and writing rules

`GET /api/ownership/rules` and `GET /api/users/search` are open to **any authenticated account**. Seeing who owns what is not a privileged operation — only changing it is. The user search is deliberately unrestricted for the same reason: the person adding a teammate to their namespace is usually a plain `member`, and requiring an instance admin for that would make delegated ownership unusable. It returns `{id, name, email}` only, needs at least 2 characters, and caps at 20 results, so it is a lookup and not a directory export.

`POST /api/ownership/rules` is an **upsert keyed on the pattern**. Posting a pattern that already has a rule replaces its member list wholesale — replace, not merge, so removing someone works through the primary verb. It returns `200`, not `201`, whether it created or replaced.

`PUT /api/ownership/rules/{id}` is keyed on the **rule id** and replaces members only. It cannot move a pattern. Rules are addressed by id precisely so a pattern never has to be escaped into a path segment, and letting a pattern change out from under an id would be the same footgun.

Members go **in** as user IDs and come **back** hydrated as `{id, name, email}`, so a client can render a rule without a round trip per member. A member whose account has since been deleted is silently skipped on the way out — not an error. The same tolerance applies when resolving skill owners for a publish: a deleted teammate is not a reason to fail someone else's publish.

**Error cases:**

- `422` — `unknown user id "..."`. An id that does not resolve to a real account is never silently dropped.
- `422` — `ownership: a rule must have at least one member`.
- `422` — an invalid pattern, e.g. `ownership: pattern "pay*ments" may only use "*" as the final character`. The pattern is validated before the permission check, so a malformed pattern reports `422` rather than `403`.
- `403` — `you may not manage pattern "payments:*"`.
- `404` — `ownership rule "..." not found`, on `PUT` and `DELETE` with an unknown id.

### Create a rule

```bash
# Find the user ids first
curl "http://localhost:8080/api/users/search?q=ada" \
  -H "Authorization: Bearer sk-..."

curl -X POST http://localhost:8080/api/ownership/rules \
  -H "Authorization: Bearer sk-..." \
  -H "Content-Type: application/json" \
  -d '{"pattern":"payments:*","members":["4f7c...","91ab..."]}'
```

Response:

```json
{
  "id": "0b2e6f1a-6f8d-4a2c-9a1f-3d5c0c1e2b77",
  "pattern": "payments:*",
  "members": [
    { "id": "4f7c...", "name": "Ada Lovelace", "email": "ada@example.com" },
    { "id": "91ab...", "name": "Grace Hopper", "email": "grace@example.com" }
  ]
}
```

### Resolve a name

```bash
curl http://localhost:8080/api/skills/payments:refunds/owners \
  -H "Authorization: Bearer sk-..."
```

```json
{
  "rule_pattern": "payments:refunds",
  "owners": [
    { "id": "91ab...", "name": "Grace Hopper", "email": "grace@example.com" }
  ],
  "unowned": false
}
```

`rule_pattern` tells you *which* rule matched, which is how you tell an exact rule from an inherited prefix. An unowned name returns `"unowned": true` with an empty `owners` array and no `rule_pattern`.

Ownership never gates reads and never re-gates a released version. Deleting a user, removing a rule, or transferring a namespace changes who reviews future changes and nothing else — a skill that worked yesterday keeps working for everyone who synced it.

## Quality scores

See [Quality scoring](/docs/quality) for what these numbers mean and where they come from.

`GET /api/skills/{name}/quality/{version}` returns the score plus the full report the evaluation produced. The report can quote the skill's own content, so its visibility depends on whether the version is released:

- For a **released** version, the report is public — its content is already visible via download/show anyway.
- For a version still **held for review**, the report is visible to instance admins only. Everyone else gets the score with `"report": null`. Note this is a role check, not an ownership check — a skill owner who is a plain `member` sees `"report": null` too.

```bash
curl http://localhost:8080/api/skills/deploy/quality/3 \
  -H "Authorization: Bearer sk-..."
```

`GET /api/skills/{name}/quality/series` groups a skill's score history into comparable runs. Two scores are only comparable if they came from the same evaluation suite and the same model panel — changing either can move the number without the skill changing at all. Scores that aren't comparable to the current run are grouped into their own series rather than mixed in.

## Contests

See [Quality scoring](/docs/quality#comparing-two-skills-contests) for what a verdict means.

`POST /api/eval/contests` queues a contest. Candidates are published versions; omitting `version` uses the released one, and omitting `suite_ref` has the worker derive a suite from every candidate.

```bash
curl -X POST http://localhost:8080/api/eval/contests \
  -H "Authorization: Bearer sk-..." -H "Content-Type: application/json" \
  -d '{"candidates":[{"skill":"payments:deploy"},{"skill":"platform:deploy"}]}'
```

`GET /api/eval/contests/{id}` returns the contest and, once it finishes, the verdict: the outcome, the split and its p-value, every task with both candidates' pass rates, and what each losing candidate missed. `suite_note` says who chose the tasks.

`GET /api/skills/{name}/contests` lists the contests a skill entered, newest first. Entries with `chain_link: true` compare two versions of that one skill, which is how improvement over time is read.

Nothing here releases a version, holds one, or clears a hold.

## Review queue

`GET /api/review/queue` lists every version currently held by the [publish gate](/docs/concepts#publish-gate), across all skills. Open to any authenticated account: a hold only its approver can see is a hold nobody discovers.

A hold is a **set of reasons**, not a state. Each row carries both:

- `hold_reasons` — every reason the version was held for, in the order recorded at publish time.
- `outstanding` — the subset with no approval recorded yet.

The two diverge as soon as one reason clears while the version stays held on another. A version held for both a scan finding and an ownership review, whose scan finding an admin has approved, reports `hold_reasons: ["scan","ownership"]` and `outstanding: ["ownership"]`. Render `outstanding` when you are asking someone to act; render `hold_reasons` when you are explaining why the version is where it is.

Each row also carries `rule_pattern`, `owners`, and `unowned`, resolved through the same code path publish uses — so the queue and a publish decision can never disagree about who owns a name.

### Deciding one

`POST /api/skills/{name}/versions/{version}/review` decides **one hold reason**, not the whole version:

```bash
curl -X POST http://localhost:8080/api/skills/deploy/versions/3/review \
  -H "Authorization: Bearer sk-..." \
  -H "Content-Type: application/json" \
  -d '{"action":"approve","reason":"reviewed the shell script by hand, false positive","hold_reason":"scan"}'
```

- `action` — `"approve"` or `"reject"`. Required.
- `reason` — the written justification, recorded on the version. Required on both actions. An override with no written justification is the one that gets forgotten.
- `hold_reason` — `"scan"` or `"ownership"`. Optional. Required when more than one reason is outstanding (omitting it returns `422` naming both); inferred when exactly one is. That is what keeps already-deployed `skael review --approve --reason "..."` calls working unchanged.

Authorization is **per reason**, not per version:

| Reason | Who may clear it |
|---|---|
| `scan` | Instance admin only. A security finding is an instance-level decision — letting a self-managed namespace owner wave one through would make the security gate as weak as the least careful namespace. |
| `ownership` | A skill owner, or an instance admin. |

The two invariants that make the review path mean anything:

- A verified quality score clears **only** `scan`, and never `ownership`. If a score could clear ownership, the whole review path would be decorative — publish an eval-passing skill into someone else's namespace and it ships.
- A skill owner clears **only** `ownership`, and never `scan`.

An instance admin can clear either, and is the only actor who can.

**Rejecting any single reason rejects the whole version.** There is no partial-reject state — a version is either still a candidate or it is not.

Other responses: `404` if the skill or version does not exist, `409` if the version exists but is not in `needs_review`, `422` for an unknown action, an empty reason, or a `hold_reason` that is not outstanding on this version.
