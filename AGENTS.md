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
- `cmd/server` holds no Docker socket and no LLM key. Only `cmd/skael-worker` runs evaluations, through the queue.
- Register every API group inside RegisterAPIRoutes in `internal/server/routes.go`.
- Call every chi Use before any route registration, or the server panics at start.
- Prefix a Huma body type with its route group. The names are global, and a collision panics at start.
- A new Huma request field needs omitempty, or deployed clients get 422. An embedded response type must be exported.
- A new Postgres interval uses make_interval with an integer, not string concatenation.
- Migrations live in `internal/platform/migrate`. Test one against a database at the prior version with MigrateUpTo.
- A server-side scan calls scan.Relativize before it decides, stores or returns the report.
- A hold is a set of reasons. A verified score clears only scan, and an owner clears only ownership. Their persisted names never change.
- The server computes a contest verdict. A worker posts only measurements.
- `internal/eval/provider` is the only place the environment becomes an LLM backend. Add a mode there, not a variable.
- Install docs never offer go install, because `go.mod` has a replace directive.
- A commit message is a subject and a short paragraph of why. Detail goes in the PR body.

## Domain words
- lift: the primary member's effectiveness minus that member's baseline, on one agent, model and task set.
- headline: the published 0-100 score, the minimum effectiveness across healthy panel members, and the only gating number.
- baseline: a session with no skill installed.
- primary member: the first entry in the model panel, and the only member a lift is computed on.
- not measured: no baseline ran. It is stored as NULL, never zero.
- contest: one job that runs two or more candidates against one suite, on one panel, on the same day.
- candidate: one skill name and version in a contest.
- verdict: a contest's outcome, a named winner or too close to call.
- too close to call: the sign test cannot separate the candidates from noise.
- chain: the contests between consecutive released versions of one skill.
- proposal: one diff across a bundle's prose that addresses every recorded loss of one candidate.
- loss: one task a candidate failed, with the missed expectations and the judge's evidence.
