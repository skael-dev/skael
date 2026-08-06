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

## skael init `<name>`

Scaffolds a spec-compliant skill directory. Creates `<name>/` in the current directory and writes a `SKILL.md` with `name`, an empty `description`, and a `metadata` block (`author`, `tags`, `version`) ready to fill in.

The name must match `^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$` — lowercase letters, digits, colons, dots and hyphens, starting and ending on a letter or digit. Colons namespace a skill (`payments:invoices`). Anything else is rejected and nothing is created.

The directory is named after the skill. If it already exists, `init` fails and writes nothing — it never merges into or overwrites an existing directory.

## skael publish `<dir>`

Validates, security-scans, packs, and uploads a skill directory.

A bundle with blocking findings meets one of two different fates, and the difference matters:

- **Refused.** Credential-theft and data-exfiltration findings — the `secret` and `exfiltration` classes at `critical` or `high` severity — are unappealable. The server returns 422, **no version row is created at all**, and nothing clears them: no evaluation, no approval, no `--override`. Fix the bundle.
- **Held.** Every other blocking finding (`execution`, `injection`, `heuristic`) holds the version for review instead. The version is created — it has a number and an archive — but it is not served to any client until it clears. A verified evaluation or a human approval releases it. See [publish gate](/docs/concepts#publish-gate).

Findings below `high` are advisory: they are reported and the version publishes normally.

The local pre-scan runs the same decision function the server does, so it aborts before upload only on a refusal. Anything the server would merely hold is uploaded, held, and reported back as held with the exact findings and what would clear each one.

A publish can also be held because you are not a [skill owner](/docs/ownership) of the name you are publishing to. That is a separate hold from a scan finding — clearing one does not clear the other, and a version held for both stays held until both are cleared.

| Flag | Default | Description |
|---|---|---|
| `--skip-local-scan` | false | Skip the local security scan and let the server decide (the server still scans independently) |
| `--override` | false | Publish despite findings that would otherwise hold the version — **instance admin required; recorded server-side** |
| `--force` | false | **Deprecated** alias for `--skip-local-scan` |

`--override` clears a **scan** hold and nothing else. It does not clear an ownership hold: publish to a name you don't own with `--override` and the version is still held, now on ownership alone. It also does not get an unappealable finding past the server — that is still a 422.

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

## skael show `<skill>`

Details for one skill: name and latest version, author, license, spec compliance, description, tags, when it was last published, and 30-day activation counts with a per-agent breakdown.

| Flag | Default | Description |
|---|---|---|
| `--versions` | false | List every version, newest first, with its age and changelog |

`--json` always includes the full version list, whether or not `--versions` was given.

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
| `--approve` | false | Approve the targeted reason |
| `--reject` | false | Reject the version |
| `--reason <text>` | — | Required. Written justification, recorded on the version |
| `--reason-kind scan\|ownership` | inferred | Which held reason this decision targets |

Exactly one of `--approve`/`--reject` is required, and `--reason` is always required. Both are checked locally before any request is made.

**`--reason` and `--reason-kind` are different things.** `--reason` is the written justification a human types and the server records. `--reason-kind` is which of the version's holds the decision is about. They are separate flags on purpose: every already-deployed `skael review --approve --reason "..."` keeps working exactly as it did.

`--reason-kind` is required when more than one reason is outstanding, and inferred when exactly one is. Naming a reason the version is not actually held for is rejected, as is any value other than `scan` or `ownership`.

Who may clear which reason:

| Reason kind | Who can clear it |
|---|---|
| `scan` | An instance admin, and only an instance admin |
| `ownership` | A [skill owner](/docs/ownership) of that name, or an instance admin |

A scan finding is an instance-level decision. If a self-managed namespace could clear one, the security gate would only ever be as strong as the least careful namespace on the instance.

Three things can come back:

- **Rejected.** `--reject` is terminal for the whole version, whichever reason it named — there is no partial reject and no way back. The command confirms the rejection and stops.
- **Approved and released.** The reason cleared was the last one outstanding. The command confirms the version is now served to clients.
- **Approved, still held.** One reason cleared but another is still outstanding — only possible when you passed `--reason-kind` explicitly. The command prints a warning naming the reason that cleared and stating the version is still held, then one `Outstanding` line per remaining reason with who can clear it. It will not say "released", because that would be a lie about a version nobody can download.

### skael review show `<skill-name>` `[version]`

Shows what a held version actually changed, before you decide on it.

Takes one or two arguments. With a version, it targets that exact held version. Without one, it picks the highest-numbered held version of that skill, and errors if no version of it is held.

It prints, in order:

- a unified diff of `SKILL.md` against the version currently being served, or `SKILL.md unchanged`
- a status list of every other file that differs — `added`, `removed`, or `modified`
- an `Outstanding:` block naming each unresolved reason and who clears it, omitted when nothing is outstanding

`--json` emits:

```json
{
  "diff": {
    "against": 3,
    "skill_md": "<unified diff>",
    "files": [{ "path": "scripts/setup.sh", "status": "added" }]
  },
  "hold_reasons": ["scan", "ownership"],
  "outstanding": ["ownership"]
}
```

`against` is the served version the diff was computed against; `0` means there is no baseline yet because this is the skill's first version. `hold_reasons` is every reason the version was ever held for; `outstanding` is the subset with no decision recorded yet. The two differ once one reason clears and the version stays held on another.

## skael owners

Manages who reviews changes to a skill name. Ownership decides who can clear an ownership hold on a publish — it never gates reads and never re-gates an already-released version. See [ownership](/docs/ownership) for how a name resolves to a rule.

A pattern is one of exactly three shapes. No mid-string globs, no character classes.

| Pattern | Matches |
|---|---|
| `payments:invoices` | that one exact skill name |
| `payments:*` | every name under the `payments:` namespace |
| `*` | every skill name on the instance |

| Command | What it does |
|---|---|
| `owners list` | Every rule and its members |
| `owners show <skill-name>` | Resolves that name, printing the rule that matched and its members — or that the name is unowned when none matches |
| `owners set <pattern> <email>...` | **Replaces** the rule's member list wholesale with exactly the emails given, creating the rule if there isn't one |
| `owners add <pattern> <email>...` | Adds members, keeping the existing ones |
| `owners rm <pattern> <email>...` | Removes members, keeping the rest |
| `owners delete <pattern>` | Deletes the rule entirely |

`add` and `rm` are not separate endpoints. Both read the current rule, compute the new member list on the client, and write it back through the same upsert `set` uses — so two people editing one rule at the same moment is last-write-wins.

**Emails are resolved to user IDs before anything is written.** Each address is looked up through the user-search API and must match a real account exactly (case-insensitively, on the whole address). One address that doesn't match aborts the entire command — including the addresses that would have resolved fine — and prints the near matches the search returned. Nothing is written. A rule that looks like it has an owner but doesn't is worse than a command that refuses to run.

`owners rm` down to zero members is refused server-side with a 422: a rule with no members claims a namespace nobody can review. Use `owners delete` to remove the rule instead.

A pattern you are not allowed to manage returns 403. You may manage one if you are an instance admin, a member of that rule, or a member of a strictly-containing rule — so a `payments:*` [namespace owner](/docs/ownership) can create `payments:invoices`, but never the reverse. Delegation only ever narrows.

`owners add` on a pattern with no rule yet creates it. `owners rm` and `owners delete` on a pattern with no rule report that no rule was found and make no write.

## skael version

Prints the version, git commit, and build date injected at build time.

A binary built with `go install` or `go run` has none of those injected and reports `dev` for the version (with `none` for the commit and `unknown` for the date). Only release builds print real values.

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
