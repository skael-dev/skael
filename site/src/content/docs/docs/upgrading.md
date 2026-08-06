---
title: Upgrading
description: How to upgrade the skael server and CLI — migrations run automatically, upgrade the server first.
---

## How upgrades work

**Server migrations are automatic.** skael uses [goose](https://github.com/pressly/goose) with embedded SQL files. On every startup, the server runs any pending migrations before accepting traffic. There is no separate migration step and no `just migrate` to remember.

First startup after an upgrade — goose applies new migrations and logs each one:

```
2026/06/12 03:40:15 OK   004_skill_aliases.sql (3.05ms)
2026/06/12 03:40:15 goose: successfully migrated database to version: 4
```

Subsequent startups when already at the current version:

```
2026/06/12 03:40:26 goose: no migrations to run. current version: 4
```

**CLI and server are independent.** The CLI has no version-negotiation handshake — it does not call `/api/capabilities` or send a version header. (The dashboard does read the server version from `/api/capabilities`, but the dashboard is served by the server itself, so there is never a version to reconcile.) Mixed versions generally work; upgrade the server first so any new API fields are available when the updated CLI calls them.

### Behavior change: roles (upgrading to a version with role support)

Before roles existed, every account but the first was created with an `admin`-equivalent role by default. The migration that introduces `owner`/`admin`/`member` roles leaves the first account (`owner`) untouched and downgrades every other existing account to `member`. On a multi-user instance this is a live change: anyone who previously relied on being able to `skael publish --override` a blocked skill loses that ability until the instance owner re-promotes them via `PUT /api/admin/users/{id}/role` or the dashboard. See [Instance roles](/docs/production#instance-roles).

### Re-install hook scripts after upgrading the CLI

Activation-tracking fixes, `event_source` labeling (see below), and the Codex hook fixes only take effect once `~/.skael/hooks/` and the agent's own hook config are rewritten with the current CLI version. **Re-run `skael hook install` (or `skael setup`) on every machine after upgrading the CLI** — the old scripts keep running as-is until you do.

This matters most for Codex users: earlier CLI versions wrote a Codex hook block under the wrong event key, so it has never actually fired. It will keep silently not firing until `skael hook install` is re-run with the fixed version.

It also affects Cursor's `event_source` labeling: Cursor's stop-hook script only started tagging its events `transcript_scan` once the fix shipped. Until the script on a given machine is regenerated, that machine's Cursor events keep posting without a label (which the server now correctly defaults to `transcript_scan` for the `cursor` agent — see the note on migrations below — but a re-installed script sending the label explicitly is still the more robust fix).

### Client IP resolution changed

If skael runs behind a reverse proxy, `TRUSTED_PROXIES` is effectively required. Left unset, every request appears to come from the proxy's own address: non-auth route classes (events, reads, writes) are largely unaffected because they're charged to a hashed API key where one is present, but the login/signup/password-reset class is keyed on IP alone — so with `TRUSTED_PROXIES` unset, your entire organization shares a single login rate-limit budget behind one ingress address. See [Production: telling skael about the proxy](/docs/production#telling-skael-about-the-proxy) for how to set it.

Separately, the request log's `ip` field changed format: it used to log the raw `RemoteAddr` (`host:port`); it now logs a bare host with the port stripped (and, behind a trusted proxy, the resolved client address instead of the proxy's). Anything parsing skael's request logs for IPs needs to account for the missing port.

### Migrations 006 and 007 rewrite `skill_events`

Both migrations run automatically at startup, inside a single transaction, and take an `ACCESS EXCLUSIVE` lock on `skill_events` for its duration: each adds a column and backfills it with an `UPDATE`, and 007 additionally validates a new `CHECK` constraint and both migrations build a new index. On a small instance this is milliseconds. On an instance with months of activation history, the lock is held roughly in proportion to the table's size — and while it's held, an old server process still serving traffic will block on any query that touches `skill_events` (activation summaries, event ingestion). Plan the upgrade window accordingly on a busy instance; see [Backup & restore](/docs/backup-restore) if you want to rehearse the timing against a copy of production data first.

## Upgrading to v0.10.0

v0.10.0 is a big release. Six things landed: skill evaluation authoring (`whetstone`), evaluation runs and scoring, an eval job queue with a new `skael-worker` binary, a publish gate, a quality section in the dashboard, and skill ownership. Nine migrations come with it, one behavior change everyone will notice, and two new optional pieces of deployment.

Read this whole section before you upgrade. Nothing here needs a config change to work, but one behavior change will surprise anyone who publishes.

### Nine new migrations (009 → 017)

They run automatically at startup, like every other migration. Each file runs inside a single transaction, so whatever lock a file takes is held until that whole file commits — not just for the statement that took it.

| Migration | What it does | Cost |
|---|---|---|
| `009_eval_queue` | Creates `eval_suites`, `eval_jobs`, `skill_quality` and their indexes | Instant — three new empty tables |
| `010_skill_quality_uplift_source` | Adds `skill_quality.uplift_source` | Instant — 009 created that table empty moments earlier |
| `011_eval_jobs_lease_seconds` | Adds `eval_jobs.lease_seconds` | Instant, same reason |
| `012_eval_suites_spec` | Adds nullable `eval_suites.spec` | Instant |
| `013_publish_gate` | Adds five gate columns to `skill_versions`, a `CHECK` constraint on `gate_state`, a partial index, and one column on `skill_quality` | **Plan for this one.** See below |
| `014_version_rendered_body` | Adds `description` and `content` to `skill_versions`, then backfills the currently-served version of every skill | **The most expensive one.** See below |
| `015_quality_report_and_job_timing` | Adds `skill_quality.report_json` and `eval_jobs.started_at` | Instant |
| `016_quality_judge_model` | Adds nullable `skill_quality.judge_model` | Instant |
| `017_skill_ownership` | Creates `ownership_rules`, `ownership_rule_members`, `version_approvals`; adds `skill_versions.hold_reasons`; backfills held versions | Near-instant. See below |

**013 takes a real lock on `skill_versions`.** Adding the columns themselves is metadata-only on Postgres 11+ — a `NOT NULL DEFAULT` no longer rewrites the table. The two statements that do work are `ADD CONSTRAINT … CHECK (gate_state IN …)`, which validates every existing row, and the `CREATE INDEX` (not `CONCURRENTLY`), which scans the table to build. Both are proportional to how many version rows you have, and because the file is one transaction, `skill_versions` is under `ACCESS EXCLUSIVE` from the first `ALTER TABLE` until the file commits.

**014 is the one to size before you start.** The `ADD COLUMN`s are free, but the backfill `UPDATE` copies each skill's rendered body onto its currently-served version row. It touches one row per *skill*, not per version — but the data it moves is the full text of every served skill, so the cost tracks your content volume rather than your row count. `skill_versions` stays under `ACCESS EXCLUSIVE` for the whole `UPDATE`, held from the `ADD COLUMN` at the top of the same transaction.

**017 looks heavier than it is.** Every constraint and index in it is on a table created in the same file, so there is nothing to validate. `hold_reasons TEXT[] NOT NULL DEFAULT '{}'` is a fast default. Its backfill only matches rows with `gate_state = 'needs_review'`, and on an upgrade from v0.9.1 there are none — `gate_state` did not exist until 013.

On a small registry all nine finish in well under a second. On one with a lot of versions or a lot of skill prose, budget for 013 and 014 and take the server down for the upgrade rather than doing this under live traffic — anything still querying `skill_versions` blocks while those locks are held. [Backup & restore](/docs/backup-restore) has what you need to rehearse the timing against a copy of production first.

### Behavior change: a blocked publish now holds the version instead of refusing it

This is the loudest change in the release.

**Before:** any `critical` or `high` scan finding refused the publish. The CLI printed the findings, the server returned an error, and no version row was ever created.

**After:** the set that refuses outright is much narrower. Only credential-theft and data-exfiltration findings — a hardcoded secret, a `/dev/tcp` reverse shell — still refuse. Those return 422, create no version row, and nothing clears them: not an evaluation, not an override, not an instance admin. Remove the finding from the bundle or it does not publish.

Every *other* blocking finding — a `curl … | bash` cradle, a prompt-injection pattern — now creates the version and **holds** it. A held version:

- has a version number and a stored archive
- does **not** advance `skills.latest_version`
- is absent from the sync manifest, so no client downloads it
- is invisible to `skael sync`, list, search and download-latest

What you will actually notice:

- `skael publish` exits differently. Instead of "security findings block publish" it prints `⏸ <name> v<n> created and held for review`, followed by what would clear each finding.
- `skills.latest_version` can be `0` on a skill that has been successfully published to. That is correct, not corruption: the skill row exists, the version exists, nothing is servable yet.
- Held versions show up in the new **Review** page in the dashboard and at `GET /api/review/queue`.

Two things clear a hold: a **verified** quality score at or above `QUALITY_FLOOR` (with a complete panel and no critical contract violations), or an instance admin approving it with `skael review <name> <version> --approve --reason "..."`.

:::caution[If you run no eval worker, the only clearing path is the human one]
The automatic path needs a `skael-worker` process and a registered evaluation suite for that skill. Without both, a held version stays held until a person approves it. That is a working configuration — it is not a broken one — but somebody has to be watching the Review page, or publishes quietly stop being served. Decide which of the two paths you are running before you upgrade, not after the first hold.
:::

`QUALITY_FLOOR` is new, defaults to `0`, and takes a number from 0 to 100. `0` means any verified report with a complete panel and no critical contract violations clears a scan hold. It is also the only server env var where a *bad value* aborts startup — every other one silently falls back to its default when it can't be parsed. That is deliberate: a security floor that silently reads as unset is a control that looks configured and does nothing.

### New optional deployment: the eval worker

`skael-worker` is a new binary in the release archives. It is entirely optional.

Without it, evaluation jobs queue and nothing is scored. That is a state the product models — skills read as `unscored`, which is a distinct thing from scoring zero — not an error condition. Nothing else degrades.

With it, you need a Docker daemon it can reach and an LLM API key. The worker claims jobs from the server, runs each evaluation in a sandboxed container, and posts the report back.

The server still holds no Docker socket and no LLM key, and that boundary does not change on upgrade. If you deploy no worker, your server's blast radius is exactly what it was in v0.9.1. See [self-hosting](/docs/self-hosting) for the worker's environment variables and how to run it.

### Skill ownership is opt-in and does nothing until you write a rule

Migration 017 creates two empty tables. Zero rules means zero behavior change — **no publish that worked before starts being held**. Ownership only holds a publish when a rule actually matches the name being published to, and an unowned name never holds. Protection switches on per namespace, when somebody writes their first rule.

One thing does happen without a rule: the first publish of a **new** name (version 1 only) records its publisher as that skill's sole skill owner, unless a rule already covers the name. Existing skills are untouched by the migration, and publishing v2 of an existing unowned skill claims nothing.

A safe rollout order:

1. Upgrade. Confirm zero rules exist (`GET /api/ownership/rules`, or the dashboard).
2. Write rules for one namespace only — the one whose team already reviews its own changes.
3. Verify it: have someone outside that rule publish to a name it covers, and confirm the version is held rather than served.
4. Widen from there.

Ownership never gates reads, and it never re-gates a version that was already released. Removing a rule, deleting a user, or transferring a namespace changes who reviews future changes and nothing else. See [Ownership](/docs/ownership).

### The release now ships four binaries

`skael` (CLI), `skael-server`, `whetstone` (evaluation authoring — see [whetstone](/docs/whetstone)), and `skael-worker`. Each has its own archive on the [releases page](https://github.com/skael-dev/skael/releases/latest), named `<binary>_<version>_<os>_<arch>.tar.gz`.

Homebrew installs `skael` and `whetstone`, each from its own formula. `skael-worker` is a binary download; the server is Docker or a binary download, as before.

## Procedure

### 1. Back up first

Back up both Postgres and the archive store before upgrading. See [Backup & restore](/docs/backup-restore). If anything goes wrong you need a consistent snapshot of both.

### 2. Stop the server

```bash
# Bare-metal / systemd
systemctl stop skael

# Docker Compose
docker compose stop server
```

### 3. Upgrade

**Docker image** — pull the new image and replace the running container:

```bash
docker pull ghcr.io/skael-dev/skael:latest
docker compose up -d server   # recreates the container with the new image
```

**Binary (GitHub releases)** — download the `skael-server` archive for your platform from the [releases page](https://github.com/skael-dev/skael/releases/latest), extract, and replace the binary:

```bash
# example: Linux amd64
VERSION=0.5.0   # replace with target version
curl -fsSL https://github.com/skael-dev/skael/releases/download/v${VERSION}/skael-server_${VERSION}_linux_amd64.tar.gz \
  | tar xz skael-server
sudo mv skael-server /usr/local/bin/skael-server
```

:::note[Homebrew ships the two CLIs only]
`brew install skael-dev/skael/skael` installs the `skael` CLI and nothing else; `whetstone` has its own formula. There is no formula for `skael-server` or `skael-worker` — both are binary downloads from GitHub releases. Use Docker or a binary download to upgrade the server.
:::

**From source** — rebuild and replace:

```bash
just build-server
# or with go directly, from a clone
go build -o skael-server ./cmd/server
```

### 4. Start and verify

Start the server. Migrations run automatically; check the logs for the goose line before proceeding.

```bash
# Bare-metal / systemd
systemctl start skael

# Docker Compose
docker compose up -d server
```

Wait a few seconds, then confirm readiness:

```bash
curl -sf http://localhost:8080/api/health/ready
```

Expected response:

```json
{"status":"ready","checks":{"database":"ok","storage":"ok"}}
```

A 503 means a dependency check failed — inspect logs before sending traffic.

### 5. Upgrade the CLI

**Homebrew:**

```bash
brew upgrade skael-dev/skael/skael
```

**Install script:**

```bash
curl -fsSL https://raw.githubusercontent.com/skael-dev/skael/main/install.sh | sh
```

**From source:**

```bash
git clone https://github.com/skael-dev/skael.git
cd skael && go build -o skael ./cmd/skael
```

`go install <pkg>@latest` does not work for any binary in this repo: `go.mod` carries a `replace` directive, and `go install` refuses a module that has one. Building from a clone is unaffected.

After upgrading the CLI, verify it can reach the server:

```bash
skael doctor
```

## Rollback

Restore the backup taken in step 1, then start the previous server version. See [Backup & restore](/docs/backup-restore) for restore commands.

Downgrading without a backup is unsupported. Down-migrations exist (`just migrate-down`) but they are a development tool for stepping back during local schema work — not a production rollback mechanism. Running them on production data risks irreversible data loss. Always restore from backup.

## Compatibility

The CLI does not call `/api/capabilities` or perform any version negotiation with the server (verified: `cli/client/client.go` sends only `X-API-Key`; no version header, no capabilities check). The dashboard does call `/api/capabilities`, to display the server version on the settings page — but it is served by the same binary, so it can never be a version behind. Mixed versions generally work.

The safe rule: **upgrade the server before the CLI.** New API fields added by the server upgrade are available when the updated CLI calls them. The reverse — an updated CLI talking to an old server — may hit endpoints or fields that do not exist yet.
