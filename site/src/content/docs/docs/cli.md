---
title: CLI reference
description: Every skael command.
---

All commands accept `--json` for scriptable output and `--no-color` to disable styled output (the `NO_COLOR` env var works too).

## Exit codes

Every `skael` command uses these codes. They are stable, so a script can branch on the code and ignore the text.

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | API or network error, or a local write that failed |
| `2` | Usage or config error: an unknown command or flag, a bad argument, or a missing or invalid config |
| `10` | Incomplete set: `skael pull` wrote nothing |
| `11` | Digest mismatch: `skael pull` or `skael add` wrote nothing |

With `--json`, a failure prints this envelope to stdout, and the exit code is not zero. `suggestion` is present only when the CLI has one. Some failures add fields, such as `excluded` on code 10 and `decision` on a blocked publish.

```json
{"error": "not configured", "code": "not_configured", "suggestion": "skael setup <url> <api-key>"}
```

A held publish is not a failure. The version exists, so `skael publish` exits 0.

`skael sync` and `skael import` exit 1 if any one skill fails. With `--json`, the counts are under `result` in the envelope.

`skael scan` also uses `1` for findings, and `2` when the scan cannot run. `skael doctor` exits `2` when the config check fails and `1` when another check fails. These two print their full report first.

## skael setup `<url> <api-key>`

One-command onboarding: validates the key, writes `~/.skael/config.json`, detects installed agents, and installs activation-tracking and auto-sync hooks.

| Flag | Default | Description |
|---|---|---|
| `--scope project\|user` | `project` | Default skill placement scope saved to config |
| `--skip-sync` | false | Skip the initial sync |
| `--skip-hooks` | false | Skip hook installation |
| `--no-auto-sync` | false | Skip auto-sync hook installation |

## skael add `<skill | pattern> | --set <name>`

Follows a skill, every released skill under a pattern, or a set, and installs what it resolves to. It records the followed source in `~/.skael/config.json`, and `skael sync` and the auto-sync hook keep it current. A new skill released under a followed pattern, or added to a followed set, arrives with no command.

```sh
skael add code-review
skael add 'payments:*'
skael add --set payments
```

A pattern is a prefix ending in `*`, such as `payments:*`, or `*` for every released skill. Quote it so the shell does not expand it. Any member can follow a pattern; it creates nothing on the server and reaches only released skills the member can already read.

When two followed sources want different versions of one skill, `add` exits 2, names both sources, and records nothing.

| Flag | Default | Description |
|---|---|---|
| `--set <name>` | none | Follow a set |
| `--scope project\|user` | config default | Override placement scope for this source |

## skael remove `<skill | pattern> | --set <name>`

Stops following a source and removes each skill that no other followed source still wants. A skill you also follow by name, or through another pattern or set, stays.

## skael sync

Resolves every followed source in `~/.skael/config.json` and installs what changed: new and updated skills, skills added to a followed set, and skills released under a followed pattern. It removes skills no source wants any more. Only changed skills are downloaded. Supports `--dry-run`.

| Flag | Default | Description |
|---|---|---|
| `--scope project\|user` | config or `project` | Override skill placement scope for this run |
| `--agent <name>` | both folders | `claude-code` writes only `.claude/skills`; any other agent writes only `.agents/skills` |
| `--dry-run` | false | Show what would happen without making changes |
| `--quiet` | false | Suppress non-error output |

## skael init `[name]`

With no argument, `init` writes the skael skill to `.agents/skills/skael/SKILL.md` in the current directory, and to `.claude/skills/skael/SKILL.md` when Claude Code is detected. The binary carries this skill, which tells an agent how to use the CLI.

- A second run changes nothing and exits 0.
- If the file differs from the skill in the binary, `init` refuses and exits 2. Pass `--force` to overwrite it.
- With `--json`, `init` prints the `path` and a `status` of `created`, `unchanged` or `overwritten`.

With a name, `init` scaffolds a spec-compliant skill directory. Creates `<name>/` in the current directory and writes a `SKILL.md` with `name`, an empty `description`, and a `metadata` block (`author`, `tags`, `version`) ready to fill in.

The name must match `^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$` — lowercase letters, digits, colons, dots and hyphens, starting and ending on a letter or digit. Colons namespace a skill (`payments:invoices`). Anything else is rejected and nothing is created.

The directory is named after the skill. If it already exists, `init` fails and writes nothing — it never merges into or overwrites an existing directory.

## skael pull `--set <name>`

Resolves a set to exact versions, downloads its bundle, and writes the skills under `<dir>/.agents/skills/`, and under `<dir>/.claude/skills/` when Claude Code is detected. It works on a clean machine with only `SKAEL_URL` and `SKAEL_KEY` set.

Pull writes once. It records nothing, and `skael sync` never follows it. To follow a set on a developer machine, use `skael add --set <name>`. To undo a pull, delete the skill folders that `manifest.json` lists.

| Flag | Default | Description |
|---|---|---|
| `--set <name>` | required | The set to pull |
| `--dir <path>` | current directory | The directory that receives `.agents/skills/` |
| `--no-hooks` | false | Write files only. Change no agent config and write nothing to `~/.skael` |

Pull runs these steps in order:

1. It resolves the set. If an entry is excluded, it prints the excluded entries and exits 10.
2. It downloads the bundle for that exact resolution.
3. It checks the SHA-256 of each skill archive and the manifest against the resolve result. A mismatch or a missing archive exits 11.
4. It unpacks each verified archive, not the bundle's unpacked tree, into a temporary directory beside the target. It refuses path traversal, links and special files, and it removes setuid bits.
5. It renames the temporary directory into place. A failure before this step leaves the target unchanged.
6. Without `--no-hooks`, it installs the activation hook for each detected agent. It does not install the auto-sync hook, because `skael sync` manages a different list of skills.

The target directory holds `manifest.json` and one directory per skill. Other entries in the target stay, such as the skill from `skael init`. A skill that the previous manifest named and this set does not name is removed.

`manifest.json` has the set name, the resolution id, and the name, version and SHA-256 of each skill. Its `archive` field names a path inside the bundle. Pull does not write the archives to disk.

## skael sets

Reads and manages named sets of skills. `--json` prints the API response unchanged. `delete` has no API body, so it prints `{"deleted": true, "name": "..."}`.

| Command | Who | Description |
|---|---|---|
| `skael sets list` | any key with `sets:read` | List sets. A set-limited key sees its own set |
| `skael sets get <name>` | any key with `sets:read` | Show the entries and the last callers |
| `skael sets resolve <name>` | any key with `sets:read` | Show exact versions, digests and excluded entries |
| `skael sets create <name> [entry...]` | owner or admin | Create a set. `--description` sets its description |
| `skael sets update <name> [entry...]` | owner or admin | Replace all entries, or change `--description`. What you omit stays |
| `skael sets delete <name>` | owner or admin | Delete a set |

Write an entry as `name` or `name@latest` to follow the latest version, or as `name@3` to pin one version. A pattern such as `'payments:*'` follows every released skill under it, and `'*'` follows every released skill. An exclusion such as `'!payments:legacy'` leaves skills out after every include. A skill a pattern matches with nothing released is skipped and the set stays complete. Saving refuses an exact entry that an exclusion also covers.

`skael sets resolve` exits 0 on an incomplete set, because that is a valid answer. Read `complete` in the output. Use `skael pull` to get exit code 10.

## skael service-accounts

Manages machine identities and their scoped keys. Every command needs an owner or admin. `<account>` is a name or an id. `--json` prints the API response unchanged. `delete` and `revoke` have no API body, so they print a small object that says what changed.

| Command | Description |
|---|---|
| `skael service-accounts list` | List service accounts |
| `skael service-accounts create <name>` | Create a service account |
| `skael service-accounts delete <account>` | Delete an account and revoke its keys |
| `skael service-accounts keys list <account>` | List an account's keys |
| `skael service-accounts keys create <account>` | Create a key and show it once |
| `skael service-accounts keys revoke <account> <key-id>` | Revoke a key |

`keys create` takes these flags:

| Flag | Default | Description |
|---|---|---|
| `--scope <scope>` | required | A scope the key holds. Repeat it for more. The scopes are `skills:read`, `sets:read`, `eval:work`, `events:write`, `outcomes:write` and `submissions:write` |
| `--set <name>` | none | Limit the key to one set. The key needs `sets:read` |
| `--name <label>` | `default` | A label for the key |

## skael webhooks

Manages [webhook](/docs/webhooks) endpoints and their deliveries. Every command needs an owner or admin. `<endpoint>` is a name or an id. If two endpoints share a name, use the id, because the name exits 2. `--json` prints the API response unchanged. `delete`, `test` and `redeliver` have no API body, so they print `{"deleted": true, ...}` or `{"queued": true, ...}`.

| Command | Description |
|---|---|
| `skael webhooks list` | List endpoints |
| `skael webhooks get <endpoint>` | Show an endpoint |
| `skael webhooks create` | Register an endpoint and show its secret once |
| `skael webhooks update <endpoint>` | Change an endpoint. What you omit stays |
| `skael webhooks delete <endpoint>` | Delete an endpoint and its delivery log |
| `skael webhooks rotate <endpoint>` | Replace the secret and show the new one once |
| `skael webhooks test <endpoint>` | Queue a test event |
| `skael webhooks deliveries <endpoint>` | List the newest 50 deliveries and their attempts |
| `skael webhooks redeliver <delivery-id>` | Send a delivery again with the same event id |

`create` and `update` take these flags. `create` needs `--name`, `--url` and `--event`.

| Flag | Description |
|---|---|
| `--name <name>` | A label for the endpoint |
| `--url <url>` | The address that receives deliveries |
| `--event <type>` | An event type. Repeat it for more. On `update`, the list replaces the current list |
| `--set <name>` | Limit `set.changed` to this set. Repeat it for more. On `update`, the list replaces the current list |

`update` also takes these flags.

| Flag | Description |
|---|---|
| `--enable` | Send deliveries to the endpoint again |
| `--disable` | Stop deliveries. The endpoint and its log stay |
| `--all-sets` | Remove the set limit, so `set.changed` fires for every set. Cannot be used with `--set` |

`rotate` takes `--window-hours <n>`, from 0 to 168. The old secret stays valid for that window, and the default is 24 hours.

A refused address or an unknown event type is a server 422, so it exits 1. A missing flag exits 2.

## skael events report `--file <path>`

Sends a batch of run events to `POST /api/events/batch`. It needs a service account key with `events:write`. The consumer comes from the account.

| Flag | Default | Description |
|---|---|---|
| `--file <path>` | required | JSON Lines with one event on each line, a JSON array of events, or an object with an `events` array |
| `--idempotency-key <key>` | none | A repeat with this key returns the first result and stores nothing |

Each event has `skill`, `version`, `agent`, `role`, `external_run_id` and `time`, and it can have `set` and `resolution_id`. A batch holds at most 1,000 events.

A batch is all or nothing. If the server rejects an event, the command prints the failing index and exits 1. With `--json`, the envelope has `index` and the code `invalid_event`. A file that cannot be read or parsed exits 2.

## skael outcomes

| Command | Who | Description |
|---|---|---|
| `skael outcomes report --run <id> --outcome <outcome>` | service account key with `outcomes:write` | Report how a run ended |
| `skael outcomes show <skill>` | any key with `skills:read` | Show the outcomes of the runs that loaded each version |

`outcomes report` takes these flags:

| Flag | Default | Description |
|---|---|---|
| `--run <id>` | required | Your id for the run |
| `--outcome <outcome>` | required | `succeeded`, `failed` or `cancelled` |
| `--repairs <n>` | 0 | How many repairs the run needed |
| `--verdict <text>` | none | The verdict of the review of the run |
| `--evidence <url>` | none | An http or https link to the evidence |
| `--set <name>` | none | The set the run used |
| `--resolution <id>` | none | The resolution the run used |

A second report for one run replaces the first, including any field you omit. The audit log keeps the old value.

`outcomes show` gives rates only when a version has at least 20 runs. It states a difference between versions and no cause.

An outcome never changes the headline and never releases a version.

## skael submissions

Sends a skill for human review and reads the decision. No command publishes or decides a submission. A person decides in the web UI. An approved submission then goes through the normal publish gate.

| Command | Who | Description |
|---|---|---|
| `skael submissions submit <dir> --name <name> --rationale <text>` | key with `submissions:write` | Pack the directory and send it. `--run-link <url>` and `--run-id <id>` link the run that produced it |
| `skael submissions list` | a person's key | List submissions. `--state` takes `pending`, `approved` or `rejected` |
| `skael submissions get <id>` | a person's key, or the consumer that sent it | Show the state and the reason |

A directory with no `SKILL.md` exits 2. An exfiltration or secret finding stores nothing and exits 1. With `--json`, the envelope has the code `submission_blocked` and the `decision`.

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

Diagnostic health check: config, connectivity, agent detection, hook status. It exits 2 when the config check fails, and 1 when another check fails. A warning does not change the exit code.

## skael hook `install` | `uninstall` | `status`

Standalone management of activation-tracking and auto-sync hooks.

- `install` — register the hooks with every detected agent (activation tracking + auto-sync)
- `uninstall` — remove the hooks from every detected agent
- `status` — show which agents have current hooks. A hook that names an old script or another skael binary shows as out of date.

Each hook runs the skael binary itself, so the same hooks work on macOS, Linux and Windows with no bash, jq or curl:

- `skael hook report --agent <name>` reads the agent's hook payload on stdin and reports a skill load. It returns at once and posts the event from a background process.
- `skael hook autosync` starts `skael sync --quiet` in the background when the last sync is 30 minutes old.

Agents call these commands. You do not run them. Both print nothing and exit 0 on every error, so a hook never blocks or breaks an agent session.

The hook names the skael on your `PATH` when that is the running binary, so a Homebrew or Scoop upgrade keeps the path. Every skael command checks the hooks you installed and rewrites one that names an old script or a moved binary.

| Agent | Activation tracking | Auto-sync |
|---|---|---|
| Claude Code | `~/.claude/settings.json`, on each Skill tool call | on each prompt |
| Codex | `~/.codex/config.toml`, a PreToolUse hook | before a tool call |
| Cursor | `~/.cursor/hooks.json`, on each read of a `SKILL.md` in `.agents`, `.cursor`, `.claude` or `.codex` `/skills/<name>/` | on session start |
| OpenCode | `~/.config/opencode/plugins/skael-tracking.ts`, on each `skill` tool call | `skael-autosync.ts`, on each new session |
| pi | `~/.pi/agent/extensions/skael-tracking.ts`, on each read of a `SKILL.md` in `.agents`, `.pi` or `.pi/agent` `/skills/<name>/`, and each `/skill:name` | `skael-autosync.ts`, on session start |

Auto-sync runs `skael sync --quiet` in the background, at most once in 30 minutes. `uninstall` removes only the files and entries that Skael wrote.

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

## skael contest `<skill>[@version] <skill>[@version] …`

Runs two or more candidate versions against one eval suite, in one job, on one panel, at the same time, and prints which won each task.

| Flag | Default | Description |
|---|---|---|
| `--suite <ref>` | — | A stored eval suite; omitted, one is derived from every candidate |
| `--tier <tier>` | `full` | Eval tier |
| `--attempts <n>` | 3 | Attempts per task per candidate |
| `--wait` | true | Wait for the verdict |

```bash
skael contest payments:deploy@3 platform:deploy@7
```

A version left off means the skill's released version. The candidates need not be the same skill.

The verdict is one of three things: a named winner, **too close to call**, or a statement that too few tasks separated the candidates for any verdict to be possible. Each comes with the split — `6-0 with 6 ties, p = 0.031` — so a reader who distrusts a single number reads the task table instead.

Two things surprise people. A 7–2 split is **not** a winner: an exact sign test puts it at p = 0.18, and at twelve tasks you need 8–1. And below six decided tasks no verdict exists at all, because even a clean sweep of five is inside what a coin does — when that happens the problem is the suite, and the command says so.

A contest is advisory. It releases nothing, holds nothing, and clears no hold. See [Quality scoring](/docs/quality#comparing-two-skills-contests).

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

### Version notice

At most once a day, a command reads the server's version from `/api/capabilities` and compares it with the CLI's. When they differ, it prints one line to stderr after the command's own output:

- An older CLI says so and shows how to update it. It keeps working; only newer features need the update.
- A newer CLI says the server needs upgrading for its newer features.

The notice never changes an exit code or stdout. It stays silent with `--json` or `--quiet`, on a `dev` build, and when the server does not answer within 2 seconds. It asks your own server only, never an outside service. `skael doctor` always shows both versions.

To update the CLI, run the install script again (`curl -fsSL https://skael.dev/install | sh`, or `irm https://skael.dev/install.ps1 | iex` on Windows), `brew upgrade --cask skael-dev/skael/skael`, or `scoop update skael`, then `skael init --force` in each repo to refresh the skael skill.

## Skill scope

Every installed skill lives at one of two scopes:

- **`user`** — installed under your home directory. Follows you across every project.
- **`project`** — installed inside the repo, under version control alongside your code. Follows the repo — everyone who checks it out gets it too.

Each skill installs into a folder named its canonical name. Windows does not allow `:` in a file name, so on Windows a prefixed skill such as `payments:refunds` installs into `payments_refunds`. The agent still sees the name in the skill's frontmatter, and activations still report `payments:refunds`. A repo that commits a `payments:refunds` folder from macOS or Linux does not check out on Windows. Commit project-scope skills from Windows, or have each developer run `skael sync`.

**The default is `project`.** Scope is resolved with this precedence: a command's `--scope` flag, then the `scope` value saved in `~/.skael/config.json`, then the default.

- `skael setup --scope user` sets the default saved to your config.
- `skael add <name> --scope user` overrides the scope for just that one skill, and that choice is remembered for it.
- `skael sync --scope ...` overrides scope for that run only — but if a skill already has its own recorded scope, that wins over the sync flag.

The project root is the nearest ancestor directory containing a `.git` folder, walking up from your current directory. If none is found, it falls back to the current directory.

Skael writes to two folders at each scope:

| Folder | user scope | project scope | Read by |
|---|---|---|---|
| Shared | `~/.agents/skills/<name>` | `<project>/.agents/skills/<name>` | Codex, Cursor, OpenCode, pi |
| Claude Code, when detected | `~/.claude/skills/<name>` | `<project>/.claude/skills/<name>` | Claude Code |

Claude Code reads only `.claude/skills`, so Skael writes a second copy there when it detects Claude Code. A skill that an older version placed in `.cursor/skills`, `.opencode/skills` or `.codex/skills` moves on the next `skael sync`.
