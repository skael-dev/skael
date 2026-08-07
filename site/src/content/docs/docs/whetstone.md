---
title: whetstone
description: The standalone CLI for drafting, linting, and evaluating skills before you publish them.
---

`whetstone` is a separate binary from `skael`. It works on local files, drafts and lints skill bundles, and runs the same evaluation engine the platform uses — all before anything reaches a registry.

It is not the registry client. That's `skael`. `whetstone` never needs a server, with one exception: `whetstone suite push`, which is the handoff point between the two.

## What it is

Two halves, in the order you use them.

**Authoring.** `spec` → `gen` → `lint` → `pack`. You describe what the skill should do in a specification, approve that document, generate the bundle from it, lint every layer (spec conformance, quality, prompt injection), and pack a spec-valid archive with the eval sidecar stripped out.

**Evaluation.** `suite` → `eval` → `drift` → `repair` → `report`. Draft a task suite, gate it on its own oracle and verifier, run a model panel against the skill, read the per-member adherence breakdown, let a repair loop propose minimal edits, and render an HTML report.

`whetstone new "<intent>"` runs the whole authoring path in one command: interview, store the spec, ask you to approve it, generate, lint, compile the drift contract, draft the suite. Everything downstream is derived from the spec, so the approval prompt is the one place review is cheap.

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
  https://github.com/skael-dev/skael/releases/download/v0.10.0/whetstone_0.10.0_darwin_arm64.tar.gz
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
whetstone suite check my-skill    # gate it on its own oracle and verifier
whetstone suite push my-skill     # register it with the server
```

`suite push` refuses if no `suite check` has been recorded for the current suite ref. The server can't tell an unchecked suite from a passing one, so it's caught here.

After the push, every `skael publish` for that skill looks up its suite and enqueues an evaluation automatically. A worker claims it, runs it, posts the report back. That's the verified path.

`whetstone eval` runs the exact same engine locally. It does **not** produce a verified score. Local runs are for your own iteration loop — a number you can act on in minutes instead of waiting on a queue. Only a report that came back through the queue counts as verified, and only a verified score can release a version the publish gate is holding.

See [Quality scoring](/docs/quality) for what a score actually measures, how to read one, and the worker's own environment.

## Command reference

### Authoring

| Command | What it does |
|---|---|
| `whetstone init` | Create a `.whetstone` workspace in the current directory |
| `whetstone doctor` | Check the agent CLI, the LLM gateway, the sandbox runtime, and the registered agent adapters |
| `whetstone new <intent>` | Interview, store a spec, approve, generate, lint, compile the drift contract, draft the suite |
| `whetstone spec show <skill>` | Print the latest stored spec and whether it's approved |
| `whetstone spec edit <skill>` | Open the spec in `$EDITOR` and store the result as a new, unapproved version |
| `whetstone spec approve <skill>` | Mark the latest stored spec version approved |
| `whetstone gen <skill>` | Regenerate the bundle from the approved spec, then lint it |
| `whetstone lint <skill\|path>` | Run spec conformance, quality, and injection lint over a bundle |
| `whetstone pack <skill\|path>` | Lint, then write a spec-valid `tar.gz` with the eval sidecar and spec stripped |
| `whetstone version` | Print version, commit, and build date |

Approval is per spec version. An edit that changes something stores a new version and drops the approval — otherwise a change that skipped the gate would inherit the last one. An edit that changes nothing stores no version at all. `gen`, `suite gen`, and `repair` all refuse to run from an unapproved spec.

`lint`'s exit code is the CI signal: 0 unless there are errors, `--strict` promotes warnings to errors. `pack` refuses to write an archive from a bundle that fails lint — an archive built from a broken bundle installs fine and fails at use time, a long way from the cause.

### Evaluation

| Command | What it does |
|---|---|
| `whetstone suite gen <skill>` | Generate and write the evaluation suite for a skill |
| `whetstone suite check <skill>` | Gate the suite on its own oracle and verifier |
| `whetstone suite push <skill>` | Upload the checked suite to a registry |
| `whetstone eval <skill>` | Run the model panel, score it, write the report |
| `whetstone drift <skill> [ref]` | Per-member adherence breakdown for one eval |
| `whetstone repair <skill>` | Cluster failures, propose minimal edits, re-evaluate until the dev split plateaus |
| `whetstone report <skill> [ref]` | Render the HTML report for one eval |

`ref` is an eval id or `latest`. It defaults to `latest`.

`suite check` asks three questions per task: does the oracle solve it, does the task's own verifier accept that solution, and does the verifier reject an untouched workspace. A task failing any of them is **void** — excluded from a later eval rather than fatal to it. Any void task exits non-zero unless you pass `--allow-void`, which is what makes it usable as a CI gate.

`repair` edits your bundle in place. It runs against the dev split only, then evaluates the holdout split exactly once — the holdout score is the reported number, never a dev-split score. The dev/holdout split seed is fixed and deliberately not a flag: re-splitting changes which tasks the repair loop was allowed to see, and makes two scores incomparable.

### Flags

| Command | Flag | Default | Description |
|---|---|---|---|
| `doctor` | `--judge` | false | Run judge calibration against the labelled set and report Cohen's κ |
| `new` | `--yes` | false | Skip the spec approval prompt |
| `lint` | `--strict` | false | Treat warnings as errors |
| `pack` | `-o, --output` | `<skill>.tar.gz` beside the bundle | Archive path |
| `suite check` | `--allow-void` | false | Exit 0 even if some tasks are void; they're still excluded from a later eval |
| `suite push` | `--endpoint` | `$SKAEL_ENDPOINT`, then `~/.skael/config.json` | Skael server URL |
| `suite push` | `--api-key` | `$SKAEL_API_KEY`, then `~/.skael/config.json` | Skael API key |
| `eval` | `--tier` | `full` | Tier to run: `smoke`, `full`, or `deep` |
| `eval` | `--agents` | shipped panel | Panel agents (pass with `--models`) |
| `eval` | `--models` | shipped panel | Panel models (pass with `--agents`) |
| `eval` | `--concurrency` | `0` | Maximum concurrent sessions; 0 uses the runner's default |
| `eval` | `--untrusted` | false | Treat the skill as untrusted; refused unless the driver is hardware-isolated |
| `eval` | `--allow-void` | false | Proceed with void tasks excluded from scoring rather than refusing |
| `eval` | `--resume` | `0` | Resume an existing eval id instead of starting a new one |
| `repair` | `--max-iter` | `3` | Maximum repair iterations |
| `repair` | `--yes` | false | Skip the repair approval prompt |
| `report` | `--open` | false | Open the rendered report with the OS default handler |

Global, on every command:

| Flag | Default | Description |
|---|---|---|
| `--json` | false | Structured JSON on stdout instead of styled output |
| `--no-color` | false | Disable color (sets `NO_COLOR`; the env var works on its own too) |

## Requirements

### Docker

`eval`, `repair`, and `suite check` need a reachable Docker daemon. Every panel session and every oracle/verifier run happens inside a sandboxed container.

`drift` and `report` do not. They read a report the store already holds, so they work on a laptop with Docker stopped.

Each of the three sandbox commands sweeps stale `whetstone-run-*` / `whetstone-proxy-*` containers and `whetstone-net-*` networks before it starts. A run killed by something stronger than its own context — SIGKILL, a crash — leaves those behind, and over a long-lived host they exhaust Docker's address pool.

Ctrl-C is handled properly: `SIGINT` and `SIGTERM` cancel the command's context, the Docker driver tears down its own containers and networks, and then the process exits. Killing whetstone harder than that skips the cleanup and leaves the leftovers for the next run's sweep.

`WHETSTONE_BASE_TAG` overrides the sandbox base image tag (default `whetstone-base:1`). You rarely want this.

### An LLM gateway

Anything that calls a model needs one: `new`, `gen`, `suite gen`, `repair`, and `doctor --judge`. `eval` uses one for its judge — without a gateway it still runs the panel, but there's no judged uplift in the report.

whetstone picks a gateway in this order:

1. `ANTHROPIC_BASE_URL` or `ANTHROPIC_AUTH_TOKEN` set — direct API, configured explicitly. An explicit gateway beats autodetection, because silently preferring a CLI that happens to be on PATH bills the wrong account and evaluates against a different model than the one you configured.
2. A supported agent CLI on PATH — billed to a subscription you already have.
3. `ANTHROPIC_AUTH_TOKEN`, then `ANTHROPIC_API_KEY` — direct API.

`ANTHROPIC_API_KEY` sits *below* the CLI on purpose. It's present on plenty of machines that also have the CLI installed.

`LLM_STRONG_MODEL` and `LLM_FAST_MODEL` override the model names, and they're the same variables the worker reads, so one environment configures both. You need them if you point `ANTHROPIC_BASE_URL` at a non-Anthropic gateway: OpenRouter namespaces its identifiers (`anthropic/claude-opus-4`), so asking it for Anthropic's bare names 404s and authoring fails with a confusing "no endpoints found". The full gateway table lives in [Quality scoring](/docs/quality#choosing-a-judge-model-and-gateway).

`whetstone doctor` tells you which of these it found and why:

```bash
whetstone doctor
```

It reports the agent CLI and its version, the selected gateway and the reason, the Docker binary, and every registered agent adapter. That last one matters: an adapter is only reachable once its package is linked in, and a forgotten import compiles clean and silently thins the panel. `doctor` never errors on a broken environment — it's the command you run *because* something is already wrong, so it diagnoses instead.

### A registry, for `suite push` only

`suite push` is the one command that talks to a server. It resolves the endpoint and key from `--endpoint`/`--api-key`, then `SKAEL_ENDPOINT`/`SKAEL_API_KEY`, then the `skael` CLI's own `~/.skael/config.json`. If you've run `skael setup`, it's already configured.

Suites are capped at 7.5MB of archive, since the server caps a whole request body at 10MB and base64 inflates by 4/3. An oversized suite fails locally with a plain message rather than an opaque 413 partway through the upload.
