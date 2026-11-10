# AGENTS.md

## What this is
Skael is a self-hostable platform and CLI that manages AI agent skills across engineering teams, with a central registry, cross-agent sync, security scanning and activation tracking.
One Go module builds every binary under `cmd/`, and `cmd/server` embeds the React SPA from `web/`.

## Run
- `cp .env.example .env` creates the local server config.
- `just db` starts Postgres, and `just dev` runs the server. Migrations run at server start.
- `just build` builds every binary into bin/. `just` lists all recipes.
- `just generate` writes the untracked OpenAPI document and web client. Run it before a web type-check or web test.
- `site/src/content/docs/docs/configuration.md` lists every server and worker variable.

## Test
- `just check` is the gate. It needs Docker.
- `just test-fast` skips testcontainers. `just test-pkg internal/scan` tests one package.
- `just test-integration` runs the real server tests. `just test-docker` runs the sandbox tests.
- `go vet -tags=integration ./...` compiles the build-tagged e2e tests. Run it after a signature change.
- `npx tsc -b` type-checks the app from web/. Plain tsc --noEmit misses the app settings.
- Run the server after a startup change. Package tests do not call server.Build.
- Run go test with -count=1. A cached pass already gave one false verification.

## Stack rules
- The repository is public. Specs, plans, status and decisions live in the private build directory, .build, with docs as its symlink. A clone without it still builds.
- Before you change an area, read its section in that directory's decisions file if it exists. Never name a private file in a commit, a PR or a tracked file.
- The web app and site use npm, not Bun.
- `cmd/server` holds no Docker socket and calls no LLM. It seals provider keys and sends them only in a job claim. Only `cmd/skael-worker` runs evaluations, through the queue.
- Register every API group inside RegisterAPIRoutes in `internal/server/routes.go`.
- Call every chi Use before any route registration, or the server panics at start.
- Prefix a Huma body type with its route group. The names are global, and a collision panics at start.
- A new Huma request field needs omitempty, or deployed clients get 422. An embedded response type must be exported.
- A new Postgres interval uses make_interval with an integer, not string concatenation.
- Migrations live in `internal/platform/migrate`. Test one against a database at the prior version with MigrateUpTo.
- A server-side scan calls scan.Relativize before it decides, stores or returns the report.
- A hold is a set of reasons. A verified score clears only scan, and an owner clears only ownership. Their persisted names never change.
- The server computes a contest verdict. A worker posts only measurements.
- `internal/eval/provider` is the only place a backend is resolved. Add a provider type on the server, not a variable.
- Install docs never offer go install, because `go.mod` has a replace directive.
- A commit message is a subject and a short paragraph of why. Detail goes in the PR body.

## Domain words
- lift: the primary member's effectiveness minus that member's baseline, on one harness, model and task set.
- headline: the published 0-100 score, the minimum effectiveness across healthy panel members, and the only gating number.
- baseline: a session with no skill installed.
- primary member: the first entry in the model panel, and the only member a lift is computed on.
- not measured: no baseline ran. It is stored as NULL, never zero.
- contest: one job that runs two or more candidates against one suite, on one panel, on the same day.
- candidate: one skill name with either a version or a PR head in a contest.
- verdict: a contest's outcome, a named winner or too close to call.
- too close to call: the sign test cannot separate the candidates from noise.
- chain: the contests between consecutive released versions of one skill.
- proposal: one diff across a skill's prose that addresses every recorded loss of one candidate.
- loss: one task a candidate failed, with the missed expectations and the judge's evidence.
- set: a named list of skills, each entry latest or one exact version. Avoid: group, collection.
- resolution: the immutable result of resolving a set, with exact versions and digests, identified by the hash of that list. Avoid: snapshot, lockfile.
- service account: a machine identity stored as a user with no password, used only through scoped keys. Avoid: bot, machine user, robot.
- bundle: the single archive of one resolution. Avoid: package, export.
- run: one automated agent session a consumer reports, unique on consumer and external run id. Avoid: execution, job.
- consumer: the system a service account acts for, taken from the account and never from a request. Avoid: client, source, integration.
- submission: a skill archive an agent sends for human review, which has no version until a person approves it. Avoid: suggestion, skill proposal, draft.
- connection: one repo, branch, root path and name prefix whose skill folders Skael syncs, publishing as its own service account. Avoid: integration, binding, repo link.
- git-bound: a skill whose source of truth is a connection. The opposite is native. Avoid: synced, managed, mirrored.
- outbound change: the PR Skael opens for a publish to a git-bound skill, which gets a version when it merges. Avoid: change request, draft.
- PR head: the stored archive of a skill folder at a PR's head SHA, which a contest can evaluate without a version. Avoid: preview version, draft version.
- provider: one named LLM backend the server stores, with a type, a base URL, an optional auth header and its key, sealed on the server, chosen by the judge and each panel member. A sign-in provider is a different thing. Avoid: LLM connection, backend, integration.
- claim code: the one-time code the server logs at first start, which the first owner must enter. Avoid: setup token, bootstrap password.
- check-in: a worker's report on each poll, idle or not, of its version, driver, harness and image digest. A heartbeat extends a job lease and is a different thing. Avoid: registration.
- sample data: rows marked as sample that "Load sample data" adds and one click removes. Avoid: demo data, seed data.
- harness: the agent CLI that runs one panel session in the sandbox, OpenCode only. Avoid: agent CLI.
- job event: one progress record a worker posts for an eval job: phase, session start or end, judge call, or cancelled. A check-in and a heartbeat are different things. Avoid: log line, progress update.
- stalled: a running job with no job event for 3 minutes, whatever its lease. Avoid: stuck, hung, dead.
- followed source: a skill name, a pattern or a set that skael add recorded, which sync keeps current. Avoid: subscription, watch.
- pattern entry: a set entry or followed source written as prefix:* or *, matching released skills by canonical name. Avoid: glob, wildcard, namespace entry.
- exclusion: a set entry written as !name or !prefix:*, applied after every include. Avoid: deny entry, blocklist.
