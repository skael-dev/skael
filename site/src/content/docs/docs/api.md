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

Every endpoint except `/api/health`, `/api/health/ready`, `/api/capabilities`, `/api/auth/signup`, `/api/auth/login`, and `/api/auth/logout` requires authentication. The three auth endpoints are exempt because you have no credentials yet when you call them. The OpenAPI spec (`/openapi.json`) is not under `/api/` and requires no key.

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

## Roles

Every account has one of three roles:

| Role | Who gets it |
|---|---|
| `owner` | The **instance owner** — the first account created, exactly one per instance, set automatically on first signup. |
| `admin` | Granted by the instance owner via `PUT /api/admin/users/{id}/role`. |
| `member` | The default for every new signup. |

Two terms are used throughout this page, because "owner" alone is ambiguous once [ownership rules](#ownership) exist:

- **instance admin** — an account whose role is `owner` or `admin`. This is the check most privileged routes make.
- **instance owner** — specifically the `owner` account. Only the `/api/admin/*` routes require it.
- A **skill owner** or **namespace owner** is a member of an ownership rule. That has nothing to do with role — a plain `member` is usually the namespace owner for their team. See [Ownership](#ownership).

Role gates these, and nothing else:

- `/api/admin/*` — instance owner only. Listing users, changing a role, resetting another user's password.
- Publishing past an appealable security finding with `?override=true` (or `skael publish --override`) — instance admin. Credential-theft and exfiltration findings are unappealable and no role overrides them.
- Clearing the `scan` hold reason on a version held for review — instance admin. The `ownership` hold reason is different; see [Review queue](#review-queue).
- The eval queue's privileged operations: `POST /api/eval/jobs/claim`, `POST /api/eval/jobs/{id}/cancel`, and `POST /api/skills/{name}/evals` — instance admin.
- Reading the full quality report of a version that is still held for review — instance admin. See [Quality scores](#quality-scores).

Everything else — publish, sync, browse, search, reading who owns what — is open to any authenticated account.

## Rate limiting

Requests are throttled per route class, each with its own per-minute budget: auth (`/api/auth/*`, default 20), event ingestion (`POST /api/events`, default 600), reads (GET/HEAD, default 300), and writes (everything else, default 60). Operators can override each via `RATE_LIMIT_AUTH` / `RATE_LIMIT_EVENTS` / `RATE_LIMIT_READ` / `RATE_LIMIT_WRITE`. Requests carrying `X-API-Key` are budgeted per key; unauthenticated or keyless requests are budgeted per source IP (auth requests are always budgeted by IP, since the key on a login/signup call is unverified).

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
| `POST` | `/api/auth/signup` | Create a user account — body requires `name`, `email`, `password` |
| `POST` | `/api/auth/login` | Log in (sets session cookie) |
| `POST` | `/api/auth/logout` | Destroy session |
| `GET` | `/api/auth/me` | Current user |
| `GET` | `/api/auth/keys` | List API keys |
| `POST` | `/api/auth/keys` | Create an API key |
| `DELETE` | `/api/auth/keys/{id}` | Delete an API key |
| `POST` | `/api/auth/change-password` | Change your own password — body requires `current_password` and `new_password` |
| `GET` | `/api/users/search` | Find users by name or email (`?q=...`). Any authenticated account; returns `{id, name, email}` only, minimum 2 characters, at most 20 results |
| `GET` | `/api/admin/users` | List all users (instance owner only) |
| `PUT` | `/api/admin/users/{id}/role` | Set a user's role to `admin` or `member` (instance owner only) — see [Roles](#roles) |
| `POST` | `/api/admin/reset-password` | Issue another user a temporary password (instance owner only) |
| `GET` | `/api/skills` | List skills (paginated) |
| `POST` | `/api/skills` | Create a skill |
| `GET` | `/api/skills/{name}` | Get a skill by name |
| `DELETE` | `/api/skills/{name}` | Delete a skill and its archives |
| `POST` | `/api/skills/register` | Register a skill stub (accepts display-style names; used by agent hooks) |
| `GET` | `/api/skills/{name}/versions` | List versions |
| `POST` | `/api/skills/{name}/versions` | Publish a new version (multipart binary, content-addressed). `?override=true` publishes despite appealable scan findings — instance admin only, recorded server-side |
| `GET` | `/api/skills/{name}/versions/{version}/download` | Download version archive |
| `GET` | `/api/skills/{name}/versions/{version}/diff` | What this version changes against the version currently served — SKILL.md as a unified diff, plus per-file added/modified/removed |
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
| `GET` | `/api/ownership/rules` | List every [ownership rule](#ownership) and its members — any authenticated account |
| `POST` | `/api/ownership/rules` | Create a rule, or replace the members of the rule already at that pattern (upsert keyed on `pattern`) |
| `PUT` | `/api/ownership/rules/{id}` | Replace one rule's members. The pattern is not editable |
| `DELETE` | `/api/ownership/rules/{id}` | Delete a rule. Returns `{"deleted": true}` |
| `GET` | `/api/skills/{name}/owners` | Who owns this name, and which rule pattern produced them |
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

- Unowned does not hold a publish. Only a *matched* rule does. Protection switches on per namespace when someone writes the first rule covering it, so upgrading an instance with no rules changes nothing.
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
  -H "X-API-Key: sk-..."

curl -X POST http://localhost:8080/api/ownership/rules \
  -H "X-API-Key: sk-..." \
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
  -H "X-API-Key: sk-..."
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
  -H "X-API-Key: sk-..."
```

`GET /api/skills/{name}/quality/series` groups a skill's score history into comparable runs. Two scores are only comparable if they came from the same evaluation suite and the same model panel — changing either can move the number without the skill changing at all. Scores that aren't comparable to the current run are grouped into their own series rather than mixed in.

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
  -H "X-API-Key: sk-..." \
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
