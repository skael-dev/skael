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

## skael import `<url|path>`

Imports skills into the registry from a GitHub repository, a subpath within one (e.g. `.../tree/main/skills/docx`), or a local directory. Each imported skill goes through the same scan and the same [publish gate](/docs/concepts#publish-gate) as `skael publish` — an imported skill can be rejected or held for review exactly like a published one.

| Flag | Default | Description |
|---|---|---|
| `--all` | false | Import everything discovered without prompting |
| `--dry-run` | false | Preview what would be imported, without importing |

Setting `GITHUB_TOKEN` on the server raises GitHub's API rate limit for repository imports.

## skael review `<skill-name> <version>`

Approves or rejects a version the publish gate is holding for review.

| Flag | Default | Description |
|---|---|---|
| `--approve` | false | Release the held version |
| `--reject` | false | Reject the held version |
| `--reason <text>` | — | Required. Written justification, recorded on the version |

Exactly one of `--approve`/`--reject` is required, and `--reason` is always required. Requires owner or admin role.

## Skill scope

Every installed skill lives at one of two scopes:

- **`user`** — installed under your home directory. Follows you across every project.
- **`project`** — installed inside the repo, under version control alongside your code. Follows the repo — everyone who checks it out gets it too.

**The default is `project`.** Scope is resolved with this precedence: a command's `--scope` flag, then the `scope` value saved in `~/.skael/config.json`, then the default.

- `skael setup --scope user` sets the default saved to your config.
- `skael add <name> --scope user` overrides the scope for just that one skill, and that choice is remembered for it.
- `skael sync --scope ...` overrides scope for that run only — but if a skill already has its own recorded scope, that wins over the sync flag.

The project root is the nearest ancestor directory containing a `.git` folder, walking up from your current directory. If none is found, it falls back to the current directory.

Each agent places skills at a different path per scope:

| Agent | user scope | project scope |
|---|---|---|
| Claude Code | `~/.claude/skills/<name>` | `<project>/.claude/skills/<name>` |
| Cursor | `~/.cursor/skills/<name>` | `<project>/.cursor/skills/<name>` |
| Codex | `~/.codex/skills/<name>` | `<project>/.agents/skills/<name>` |
| OpenCode | `~/.config/opencode/skills/<name>` | `<project>/.opencode/skills/<name>` |

Codex is the odd one out: its project-scope path is `.agents/skills`, not `.codex/skills`. Every other agent uses the same directory name for both scopes.
