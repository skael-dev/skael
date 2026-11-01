---
name: skael
description: Use the skael CLI to find, install, pull and publish agent skills from a Skael registry, and to read its exit codes. Use when a task mentions skael, a skill set, a skill registry, or when you need a skill this repo does not have.
---

# skael

`skael` is the command line for a Skael registry. The registry holds versioned, security-scanned agent skills.

## Before you start

Run `skael doctor`. It reports the config, the server and each agent.

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

## Install a skill for this developer

```sh
skael add <skill>
skael sync
skael remove <skill>
```

`add` and `sync` write to each detected agent's skills directory.

## Pull a set

A set is a named list of skills. Each entry is the latest version or one exact version. Use a set when a repo, a run or a machine needs a known group of skills.

```sh
skael sets list
skael sets get <name>
skael sets resolve <name>
skael pull --set <name> --dir . --no-hooks
```

`pull` writes each skill to `<dir>/.agents/skills/<skill>/`, and it writes `manifest.json` beside them. The manifest records the exact versions and digests. It writes every skill or none.

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

## Manage sets and machine identities

These commands need an owner or admin key.

```sh
skael sets create <name> <skill> <skill>@latest <skill>@3 --description "..."
skael sets update <name> <skill>@4
skael sets delete <name>

skael service-accounts create <name>
skael service-accounts keys create <account> --scope sets:read --set <set>
skael service-accounts keys revoke <account> <key-id>
```

The scopes are `skills:read`, `sets:read` and `eval:work`. The server shows a new key once. Give it to the person who asked for it, and do not store it.

## Keep this skill current

`skael init` writes this file to `.agents/skills/skael/SKILL.md`. It never overwrites a changed copy unless you pass `--force`.
