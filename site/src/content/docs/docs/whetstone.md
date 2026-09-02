---
title: whetstone
description: The standalone CLI for drafting, linting, and evaluating skills before you publish them.
---

`whetstone` is a separate binary from `skael`. It works on local files, drafts and lints skill bundles, and runs the same evaluation engine the platform uses — all before anything reaches a registry.

It is not the registry client. That's `skael`. `whetstone` never needs a server, with one exception: `whetstone suite push`, which is the handoff point between the two.

## What it is

Two halves, in the order you use them.

**Authoring.** `spec` → `gen` → `lint` → `pack`. You describe what the skill should do in a specification, approve that document, generate the bundle from it, lint every layer (spec conformance, quality, prompt injection), and pack a spec-valid archive with the eval sidecar stripped out.

**Evaluation.** `suite` → `eval` → `report`. Draft an eval set, run a model panel against the skill, and render an HTML report. A score is the share of expectations the panel's sessions passed, graded by a model against `evals/evals.json`.

`whetstone new "<intent>"` runs the whole authoring path in one command: interview, store the spec, approve it, generate, lint, and draft the eval set. The bundle and the eval set are drafted at the same time, and so are the passes inside the bundle, which is most of why the command finishes in about half the calls' worth of wall clock it used to. It never stops to ask. Everything downstream is derived from the spec, so the run prints the spec it stored and names the commands that change it — `whetstone spec edit`, then `whetstone gen`.

Everything lives in a `.whetstone` workspace. Commands walk up from the working directory to find it, the way git does. `whetstone init` creates one and refuses to create a nested one — a nested workspace shadows the outer one for every later command, and a mistyped path silently becoming a fresh empty workspace looks exactly like a lost skill.

## Installing it

whetstone has its own Homebrew formula, separate from `skael`. Installing the CLI does not install whetstone, and neither does the curl installer — that one is `skael`-only by design.

**Homebrew.**

```bash
brew install skael-dev/skael/whetstone
```

**Release archive.** Every release ships `whetstone` as its own tarball, same os/arch matrix as the other binaries — `linux`, `darwin`, `windows` × `amd64`, `arm64`. `.tar.gz` everywhere except Windows, which is a `.zip`.

```bash
curl -fsSL -o whetstone.tar.gz \
  https://github.com/skael-dev/skael/releases/download/v0.13.0/whetstone_0.13.0_darwin_arm64.tar.gz
tar -xzf whetstone.tar.gz whetstone
sudo mv whetstone /usr/local/bin/
```

On Windows, download `whetstone_<version>_windows_amd64.zip` (or `_arm64`) from the [releases page](https://github.com/skael-dev/skael/releases) and put `whetstone.exe` somewhere on your `PATH`.

**From source.**

```bash
git clone https://github.com/skael-dev/skael.git
cd skael && go build -o whetstone ./cmd/whetstone
```

`just build` puts all four binaries — `skael-server`, `skael`, `whetstone`, `skael-worker` — in `bin/`.

`go install github.com/skael-dev/skael/cmd/whetstone@latest` does **not** work, and will not until a future release. `go.mod` carries a `replace` directive pinning a transitive dependency, and `go install <pkg>@version` refuses any module that has one — it fails with "the go.mod file for the module … contains one or more replace directives". Building from a clone is unaffected, which is why the recipe above uses `go build`.

Check it worked:

```bash
whetstone version
whetstone doctor
```

## How it relates to quality scores

Three binaries, easy to confuse. The split:

- **`whetstone`** — local, author-facing. Your machine, your loop, your credentials.
- **`skael-worker`** — server-side. The only thing that produces a **verified** score.
- **`skael`** — the registry client. Publishes, syncs, installs.

A skill is only scored if it has a registered evaluation suite. No suite means no score, permanently — not a pending state, not a zero, just nothing. That's deliberate: scoring against a suite the author never signed off on measures the wrong thing.

whetstone is the only thing that produces a suite:

```bash
whetstone suite gen my-skill      # draft it from the approved spec
whetstone suite check my-skill    # report which evals cannot be scored, and why
whetstone suite push my-skill     # register it with the server
```

`suite check` is a report, not a gate you have to pass first. `suite push` and `eval` run the same check themselves, so neither depends on you having run it.

It also declares whether anybody has read the eval set. whetstone records the content hash of what it generated; `suite push` compares the current hash against it. An untouched set is pushed as machine-derived, and the server records it that way. The cost is real: a score against a machine-derived set is still a quality signal, but it cannot release a version the publish gate is holding. A skill must not write its own exam. Open `evals/triggers.json`, read it, change what is wrong — any edit at all clears the flag — or use the review view in the web UI, where an owner or admin can mark the set reviewed without editing a line. `whetstone tune` counts as a machine writer too, so a tuned set stays machine-derived until somebody reads it.

After the push, every `skael publish` for that skill looks up its suite and enqueues an evaluation automatically. A worker claims it, runs it, posts the report back. That's the verified path.

`whetstone eval` runs the exact same engine locally. It does **not** produce a verified score. Local runs are for your own iteration loop — a number you can act on in minutes instead of waiting on a queue. Only a report that came back through the queue counts as verified, and only a verified score can release a version the publish gate is holding.

See [Quality scoring](/docs/quality) for what a score actually measures, how to read one, and the worker's own environment.

## Command reference

### Authoring

| Command | What it does |
|---|---|
| `whetstone init` | Create a `.whetstone` workspace in the current directory |
| `whetstone doctor` | Check the agent CLI, the LLM gateway, the sandbox runtime, and the registered agent adapters |
| `whetstone new <intent>` | Interview, store a spec, approve, generate, lint, and draft the eval set |
| `whetstone spec show <skill>` | Print the latest stored spec and whether it's approved |
| `whetstone spec edit <skill>` | Open the spec in `$EDITOR` and store the result as a new, approved version |
| `whetstone spec approve <skill>` | Mark the latest stored spec version approved |
| `whetstone gen <skill>` | Regenerate the bundle from the approved spec, then lint it |
| `whetstone lint <skill\|path>` | Run spec conformance, quality, and injection lint over a bundle |
| `whetstone pack <skill\|path>` | Lint, then write a spec-valid `tar.gz` with the eval sidecar and spec stripped |
| `whetstone version` | Print version, commit, and build date |

Approval is per spec version, and both writers approve what they write. `new` approves the spec it drafted. `spec edit` approves what you saved, because you are the author of that document and it carries more review than the drafted one `new` already accepted. An edit that changes nothing stores no version at all. `gen` and `suite gen` both refuse to run from an unapproved spec, which is what `spec approve` is for after a version arrives some other way.

`lint`'s exit code is the CI signal: 0 unless there are errors, `--strict` promotes warnings to errors. `pack` refuses to write an archive from a bundle that fails lint — an archive built from a broken bundle installs fine and fails at use time, a long way from the cause.

### Evaluation

| Command | What it does |
|---|---|
| `whetstone suite gen <skill>` | Generate and write the evaluation suite for a skill |
| `whetstone suite check <skill>` | Report which evals cannot be scored, and why |
| `whetstone suite push <skill>` | Upload the eval set to a registry |
| `whetstone tune <skill>` | Tune the description for triggering accuracy against the trigger set |
| `whetstone eval <skill>` | Run the model panel, score it, write the report |
| `whetstone report <skill> [ref]` | Render the HTML report for one eval |

`ref` is an eval id or `latest`. It defaults to `latest`.

`suite check` asks whether each eval can produce a measurement at all: an eval with nothing to grade, or one naming an input file the set does not carry, is **void** — excluded from a later eval rather than fatal to it. Any void eval exits non-zero unless you pass `--allow-void`, which is what makes it usable as a CI gate. The check is pure and takes microseconds, so nothing is stored: `eval` and `suite push` repeat it for themselves.

`tune` measures how often a model consults your skill for the queries in `evals/triggers.json`, proposes a better description from what failed, and keeps the one that scores best on the queries it was never tuned against. It holds a fraction of the set back for exactly that reason: a description that wins the queries which tuned it is a description fitted to them. A short trigger set is topped up and written back, so the eval tiers read the grown set too. With `--apply` (the default) the winner is stored as a new approved spec version and written into `SKILL.md`. Confirm it against real agent sessions with `whetstone eval` afterwards — `tune` measures a model's selection decision, not an agent's, so the two can disagree.

`eval` reuses a baseline session from an earlier run when the suite, the eval, the agent, the model and the agent's version all match, the earlier run succeeded, and it is under 30 days old. A baseline installs no skill, so re-running it measures nothing new — at the full tier the baselines are 10 of 36 sessions. Pass `--fresh-baseline` to run them anyway.

### Flags

| Command | Flag | Default | Description |
|---|---|---|---|
| `doctor` | `--judge` | false | Run judge calibration against the labelled set and report Cohen's κ |
| `lint` | `--strict` | false | Treat warnings as errors |
| `pack` | `-o, --output` | `<skill>.tar.gz` beside the bundle | Archive path |
| `suite check` | `--allow-void` | false | Exit 0 even if some evals are void; they're still excluded from a later eval |
| `suite push` | `--endpoint` | `$SKAEL_ENDPOINT`, then `~/.skael/config.json` | Skael server URL |
| `suite push` | `--api-key` | `$SKAEL_API_KEY`, then `~/.skael/config.json` | Skael API key |
| `tune` | `--queries` | `16` | Trigger queries to tune against; a short set is topped up and written back |
| `tune` | `--runs` | `2` | Runs per query; at one run the loop rewrites a description to fix a coin flip |
| `tune` | `--iterations` | `3` | Maximum improvement iterations |
| `tune` | `--holdout` | `0.4` | Fraction of the set held out for selection; 0 disables it |
| `tune` | `--threshold` | `0.5` | Trigger rate at which a query counts as fired |
| `tune` | `--concurrency` | `0` | Maximum concurrent model calls; 0 uses the default |
| `tune` | `--apply` | true | Write the winner to the spec and to `SKILL.md` |
| `eval` | `--tier` | `full` | Tier to run: `smoke`, `full`, or `deep` |
| `eval` | `--agents` | shipped panel | Panel agents (pass with `--models`) |
| `eval` | `--models` | shipped panel | Panel models (pass with `--agents`) |
| `eval` | `--concurrency` | `0` | Maximum concurrent sandbox sessions; 0 uses the runner's default of 6 |
| `eval` | `--grade-concurrency` | `0` | Maximum concurrent judge calls; 0 uses 8 |
| `eval` | `--fresh-baseline` | false | Run every baseline session instead of reusing a matching one from an earlier eval |
| `eval` | `--untrusted` | false | Treat the skill as untrusted; refused unless the driver is hardware-isolated |
| `eval` | `--allow-void` | false | Proceed with void tasks excluded from scoring rather than refusing |
| `eval` | `--resume` | `0` | Resume an existing eval id instead of starting a new one |
| `report` | `--open` | false | Open the rendered report with the OS default handler |

Global, on every command:

| Flag | Default | Description |
|---|---|---|
| `--json` | false | Structured JSON on stdout instead of styled output |
| `--no-color` | false | Disable color (sets `NO_COLOR`; the env var works on its own too) |

## Requirements

### Docker

`eval` needs a reachable Docker daemon. Every panel session happens inside a sandboxed container.

`suite check` and `report` do not. One is a pure check over local files, the other reads a report the store already holds, so both work on a laptop with Docker stopped.

`eval` sweeps stale `whetstone-run-*` / `whetstone-proxy-*` containers and `whetstone-net-*` networks before it starts. A run killed by something stronger than its own context — SIGKILL, a crash — leaves those behind, and over a long-lived host they exhaust Docker's address pool.

Ctrl-C is handled properly: `SIGINT` and `SIGTERM` cancel the command's context, the Docker driver tears down its own containers and networks, and then the process exits. Killing whetstone harder than that skips the cleanup and leaves the leftovers for the next run's sweep.

`WHETSTONE_BASE_TAG` overrides the sandbox base image tag (default `whetstone-base:1`). You rarely want this.

### An LLM gateway

Anything that calls a model needs one: `new`, `gen`, and `suite gen`. `eval` needs one too — the score is an expectation pass rate, and a model grades every expectation, so a run without a gateway refuses rather than reporting a partial number.

whetstone picks a gateway in this order:

1. `ANTHROPIC_BASE_URL` or `ANTHROPIC_AUTH_TOKEN` set — direct API, configured explicitly. An explicit gateway beats autodetection, because silently preferring a CLI that happens to be on PATH bills the wrong account and evaluates against a different model than the one you configured.
2. A supported agent CLI on PATH — billed to a subscription you already have.
3. `ANTHROPIC_AUTH_TOKEN`, then `ANTHROPIC_API_KEY` — direct API.

`ANTHROPIC_API_KEY` sits *below* the CLI on purpose. It's present on plenty of machines that also have the CLI installed.

`LLM_MODEL` overrides the model names — comma-separated, most capable first. It is the same variable the worker reads, resolved by the same code, so one environment configures both. You need it if you point `ANTHROPIC_BASE_URL` at a non-Anthropic gateway: OpenRouter namespaces its identifiers (`anthropic/claude-sonnet-5`), so asking it for Anthropic's bare names 404s and authoring fails with a confusing "no endpoints found". The full gateway table lives in [Quality scoring](/docs/quality#choosing-a-model-and-a-gateway).

`whetstone doctor` tells you which of these it found and why:

```bash
whetstone doctor
```

It reports the agent CLI and its version, the selected gateway and the reason, the Docker binary, and every registered agent adapter. That last one matters: an adapter is only reachable once its package is linked in, and a forgotten import compiles clean and silently thins the panel. `doctor` never errors on a broken environment — it's the command you run *because* something is already wrong, so it diagnoses instead.

### A registry, for `suite push` only

`suite push` is the one command that talks to a server. It resolves the endpoint and key from `--endpoint`/`--api-key`, then `SKAEL_ENDPOINT`/`SKAEL_API_KEY`, then the `skael` CLI's own `~/.skael/config.json`. If you've run `skael setup`, it's already configured.

Suites are capped at 7.5MB of archive, since the server caps a whole request body at 10MB and base64 inflates by 4/3. An oversized suite fails locally with a plain message rather than an opaque 413 partway through the upload.
