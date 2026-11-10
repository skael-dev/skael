---
name: skael
description: Use the skael CLI to find, install, pull and publish agent skills from a Skael registry, and to read its exit codes. Use when a task mentions skael, a skill set, a skill registry, or when you need a skill this repo does not have.
---

# skael

`skael` is the command line for a Skael registry. The registry holds versioned, security-scanned agent skills.

## Before you start

Run `skael doctor`. It reports the config, the server and each agent. `skael version` prints the CLI version.

The full command reference is at https://skael.dev/docs/cli. Read it for a flag this skill does not list, rather than guessing.

The CLI reads `SKAEL_URL` and `SKAEL_KEY`. If either is absent, it reads `~/.skael/config.json`, which `skael setup <url> <api-key>` writes. Never print a key. Never write a key into a file in the repo.

## Exit codes

Branch on the exit code. Do not parse error text.

| Code | Meaning | What to do |
|---|---|---|
| 0 | Success | Continue. |
| 1 | API, network or local write error. `skael scan` also uses 1 for findings. | Read the error. Retry only if the cause is the network. |
| 2 | Usage or config error | Fix the command or the config. A retry gives the same result. |
| 10 | Incomplete set | `skael pull` wrote nothing. Report the excluded entries. Do not remove them from the set by yourself. |
| 11 | Digest mismatch | `skael pull` wrote nothing. Stop and tell a person. This can mean tampering. Do not retry in a loop. |

## JSON output

Add `--json` to any command when you parse the output. A read of the API prints the API's own shape. A failure prints this to stdout, and the exit code is non-zero:

```json
{"error": "what failed", "code": "machine_code", "suggestion": "what to try"}
```

## Find a skill

```sh
skael search "<words>"
skael list
skael show <skill> --versions
```

## Follow skills on this machine

`skael add` follows a skill, a pattern or a set, and installs what it resolves to. `skael sync` and the auto-sync hook keep everything followed current, so a new skill released under a followed pattern, or added to a followed set, arrives with no command.

```sh
skael add <skill>
skael add 'payments:*'        # every released skill under payments:
skael add --set <name>
skael sync
skael remove <skill>          # or 'payments:*', or --set <name>
```

Quote a pattern so the shell does not expand it. Skills go to `.agents/skills`, and also to `.claude/skills` when Claude Code is detected. `--scope user` writes under your home folder instead of the repo.

`remove` stops following a source. A skill another followed source still wants stays.

When two followed sources want different versions of one skill, `add` exits 2 and names both. Remove one of them; do not pin around it.

## Write a set once

A set is a named list of skills. An entry is a skill at its latest version, a skill at one exact version, a pattern such as `payments:*`, `*` for every released skill, or an exclusion such as `!payments:legacy`. Use `pull` when a repo, a CI run or a machine needs a known group of skills written once.

```sh
skael sets list
skael sets get <name>
skael sets resolve <name>
skael pull --set <name> --dir . --no-hooks
```

`pull` writes each skill to `<dir>/.agents/skills/<skill>/`, and to `<dir>/.claude/skills/` when Claude Code is detected, with `manifest.json` beside them. The manifest records the exact versions and digests. It writes every skill or none. `pull` records nothing: sync never follows it. To follow a set, use `skael add --set`.

To undo a pull, delete the skill folders it wrote; `manifest.json` lists them. The next `pull` of the set also removes skills that left it.

Use `--no-hooks` on a machine or in CI. It writes files only and changes no agent config. Omit it on a developer machine to install activation tracking too.

`sets resolve` exits 0 on an incomplete set. Read `complete` in its output. `pull` exits 10.

## Publish a skill

```sh
skael init <name>
skael scan <dir>
skael publish <dir>
```

Write the description in `SKILL.md` before you publish. Run `scan` first: code 0 is clean, 1 means findings, and 2 means the scan did not run.

`publish` has three results:

- Published: the version is live.
- Held: the version exists but is not served. The command exits 0 and names who can clear it. Do not retry. A person runs `skael review`.
- Blocked: no version exists, and the command exits 1. Remove the finding. No override clears a credential or exfiltration finding.

Never use `--override` or `--skip-local-scan` unless a person asks you to.

## Report a run

These commands need a service account key. The consumer comes from the account.

```sh
skael events report --file events.jsonl --idempotency-key <key>
skael outcomes report --run <id> --outcome succeeded --repairs 1 --evidence <url>
skael outcomes show <skill>
```

Use one idempotency key for each batch, and reuse it on a retry. A batch is all or nothing. On a rejected event, the command exits 1 and names the failing index. Fix that event and resend the whole batch.

`--outcome` is `succeeded`, `failed` or `cancelled`. A second report for one run replaces the first. An outcome never changes the headline and never releases a version.

## Submit a skill for review

Use this when you have a new skill or a change and no person asked you to publish.

```sh
skael submissions submit <dir> --name <skill> --rationale "..." --run-link <url> --run-id <id>
skael submissions get <id>
```

A submission publishes nothing. A person decides it in the web UI. No command decides a submission, so do not look for one. Read `state` and `reason` with `get`. A rejection states its reason: fix that and submit again. An exfiltration or secret finding stores nothing and exits 1.

## Manage sets and machine identities

These commands need an owner or admin key.

```sh
skael sets create <name> <skill> <skill>@3 'payments:*' '!payments:legacy' --description "..."
skael sets update <name> <skill>@4
skael sets delete <name>

skael service-accounts create <name>
skael service-accounts keys create <account> --scope sets:read --set <set>
skael service-accounts keys revoke <account> <key-id>
```

The scopes are `skills:read`, `sets:read`, `eval:work`, `events:write`, `outcomes:write` and `submissions:write`. The server shows a new key once. Give it to the person who asked for it, and do not store it.

## Other commands

These exist. Read https://skael.dev/docs/cli before you use one.

- `skael import <url|path>` imports skills from a GitHub repo or a local folder.
- `skael contest <skill>@<v> <skill>@<v>` runs candidates against one suite and names a winner or "too close to call".
- `skael owners show|set|add|rm` reads and changes who reviews a skill name or namespace.
- `skael review <skill> <version>` shows or decides a held version. Decide one only when a person asks you to.
- `skael hook install|status|uninstall` manages activation tracking in each agent.
- `skael webhooks` manages webhook endpoints. It needs an owner or admin key.

## Keep this skill current

`skael init` writes this file to `.agents/skills/skael/SKILL.md`, and to `.claude/skills/skael/SKILL.md` when Claude Code is detected. It never overwrites a changed copy unless you pass `--force`.

To update the CLI, run the install script again (`curl -fsSL https://skael.dev/install | sh`) or `brew upgrade --cask skael-dev/skael/skael`. Then run `skael init --force` to refresh this file to the version that ships with the new CLI.
