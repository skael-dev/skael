---
title: CLI reference
description: Every skael command.
---

All commands accept `--json` for scriptable output and `--no-color` to disable styled output (the `NO_COLOR` env var works too).

## skael setup `<url> <api-key>`

One-command onboarding: validates the key, writes `~/.skael/config.json`, detects installed agents, runs the first sync, and installs activation-tracking hooks.

| Flag | Default | Description |
|---|---|---|
| `--scope project\|user` | `project` | Default skill placement scope saved to config |
| `--skip-sync` | false | Skip the initial sync |
| `--skip-hooks` | false | Skip hook installation |

## skael sync

Pulls the latest skills from the platform and places them in every detected agent's directory. Only changed skills are downloaded. Supports `--dry-run`.

| Flag | Default | Description |
|---|---|---|
| `--scope project\|user` | config or `project` | Override skill placement scope for this run |
| `--agent <name>` | all detected | Sync only for the named agent |
| `--dry-run` | false | Show what would happen without making changes |
| `--quiet` | false | Suppress non-error output |

## skael publish `<dir>`

Validates, security-scans, packs, and uploads a skill directory. Blocked on critical/high findings.

| Flag | Default | Description |
|---|---|---|
| `--force` | false | Publish even with critical findings — **bypasses the security gate; use with caution** |

## skael scan `<dir>`

Runs the security scan locally without publishing.

**Exit codes:**

| Code | Meaning |
|---|---|
| `0` | No findings |
| `1` | Findings detected (warn or critical) |
| `2` | Scan could not run (missing SKILL.md, I/O error) |

## skael search `<query>`

Full-text search across the registry (with fuzzy matching on names).

## skael list

Lists all skills on the platform.

## skael doctor

Diagnostic health check: config, connectivity, agent detection, hook status.

## skael hook `install` | `uninstall` | `status`

Standalone management of the activation-tracking hooks.

- `install` — write hook scripts for all detected agents
- `uninstall` — remove hook scripts from all detected agents
- `status` — show which agents have hooks installed

## skael import `<source>`

Imports skills from an external source (e.g. a GitHub repository) into your registry, scanning on the way in.
