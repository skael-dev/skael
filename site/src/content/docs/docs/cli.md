---
title: CLI reference
description: Every skael command.
---

All commands accept `--json` for scriptable output and `--no-color` to disable styled output (the `NO_COLOR` env var works too).

## skael setup `<url> <api-key>`

One-command onboarding: validates the key, writes `~/.skael/config.json`, detects installed agents, and installs activation-tracking and auto-sync hooks.

| Flag | Default | Description |
|---|---|---|
| `--scope project\|user` | `project` | Default skill placement scope saved to config |
| `--skip-sync` | false | Skip the initial sync |
| `--skip-hooks` | false | Skip hook installation |
| `--no-auto-sync` | false | Skip auto-sync hook installation |

## skael add `<name>`

Installs a skill from the registry. Downloads the latest version, verifies the checksum, extracts to all detected agent directories, and adds the skill to `~/.skael/config.json`.

| Flag | Default | Description |
|---|---|---|
| `--scope project\|user` | config default | Override skill placement scope |

## skael remove `<name>`

Uninstalls a skill. Removes files from agent directories and removes the skill from `~/.skael/config.json`.

## skael sync

Updates installed skills to the latest versions from the platform. Only skills listed in `~/.skael/config.json` are synced — not the full registry. Only changed skills are downloaded. Supports `--dry-run`.

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
| `--skip-local-scan` | false | Skip the local security scan and let the server decide (the server still scans independently) |
| `--override` | false | Publish despite blocking findings — **owner or admin role required; recorded server-side** |
| `--force` | false | **Deprecated** alias for `--skip-local-scan` |

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

Lists all skills on the platform. Use `--installed` to show only locally installed skills with their scope and version.

## skael doctor

Diagnostic health check: config, connectivity, agent detection, hook status.

## skael hook `install` | `uninstall` | `status`

Standalone management of activation-tracking and auto-sync hooks.

- `install` — write hook scripts for all detected agents (activation tracking + auto-sync)
- `uninstall` — remove hook scripts from all detected agents
- `status` — show which agents have hooks installed

## skael import `<source>`

Imports skills from an external source (e.g. a GitHub repository) into your registry, scanning on the way in.
