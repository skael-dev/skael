---
name: skael-evals
description: Write an eval suite for an agent skill in the skill-creator layout (evals/evals.json, evals/triggers.json, evals/files/, evals/checks/) that `skael eval check` reports at the full tier. Use when asked to write evals, tests or an eval suite for a skill, or to measure a skill with Skael.
---

# skael-evals

A suite is the exam a skill is scored against. It lives in the skill folder, at `evals/`:

```
my-skill/
  SKILL.md
  evals/
    evals.json      tasks, expectations and checks
    triggers.json   prompts the skill must and must not fire on
    files/          input files a task gives the agent
    checks/         scripts a command check runs
```

`skael publish` stores `evals/` as the skill's suite. Installers never receive it, and the agent under test never sees it.

## Steps

1. Read the skill's `SKILL.md` and every file it links to. List each thing the skill claims to do.
2. Write at least 12 evals in `evals/evals.json`. The full tier needs 10 that can be scored, so 12 leaves room for one you get wrong. Start from `templates/evals.json` in this skill.
3. Write each eval's expectations. Read `expectations.md` in this skill first.
4. Turn each mechanical expectation into a check. See "Checks" below.
5. Write `evals/triggers.json` with at least 4 prompts that must fire the skill and 4 that must not. Start from `templates/triggers.json`.
6. Run `skael eval check <skill-dir> --tier full`. Fix every line that starts with ✗. Run it again until it prints `tier: full` and exits 0.
7. Stop. Publish only if the user asks: `skael publish <skill-dir>`, then `skael eval run <skill> --wait`.

`examples/code-review/evals/` in this skill is a complete suite that reaches the full tier. Read it before you write your first eval.

## Evals

Each eval is one request a real user would make, typed the way that user types it.

- `id`: a unique integer.
- `prompt`: the request. Name the files and give concrete detail. Do not name the skill: the agent must find it.
- `expected_output`: one or two sentences on what success looks like. The judge reads it.
- `files`: input files, as `evals/files/<name>`. Each file is copied to the root of an otherwise empty workspace, under its base name. Two files in one eval must not share a base name.
- `expectations`: statements the judge grades as true or false against the transcript and the files the agent wrote.
- `checks`: statements graded without a judge.

Cover the ordinary case first, then variations, then edges. Include one task where the right answer is "nothing is wrong" or "do less", so the score punishes invented work.

## Checks

A check runs in the sandbox after the agent finishes, with the network off. It counts as one expectation. An eval with only checks makes no judge call, so its score does not depend on the judge model.

| Type | Fields | Passes when |
|---|---|---|
| `file_exists` | `path` | the file exists in the workspace |
| `regex` | `path`, `pattern` | the file's content matches the Go RE2 pattern |
| `json_schema` | `path`, `schema` | the file is JSON that matches the JSON Schema |
| `command` | `script` | `evals/checks/<script>` exits 0 |

Add a `name` to every check. The report and each loss show it.

```json
"checks": [
  {"name": "report written", "type": "file_exists", "path": "report.md"},
  {"name": "names the total", "type": "regex", "path": "report.md", "pattern": "(?i)total revenue"},
  {"name": "valid output", "type": "json_schema", "path": "out.json", "schema": {"type": "object", "required": ["rows"]}},
  {"name": "tests pass", "type": "command", "script": "run-tests.sh"}
]
```

A command script runs from the workspace root, as a user that is not root, with a timeout. The agent cannot read it: it is copied in only after the agent finishes. Its output tail is the evidence on a failure, so print what failed. Start the script with `#!/bin/sh` and use only tools the sandbox has: sh, git, python3, node, jq, make. Use `git -c safe.directory='*'` for git commands.

Use a check for what a machine can decide: a file exists, a file contains a string, JSON has a shape, tests pass, git history is unchanged. Use an expectation for what needs judgement: the finding is correct, the fix is right, the tone fits.

## Triggers

`evals/triggers.json` is an array of `{"query": "...", "should_trigger": true}`. The full tier needs 3 of each kind, and the deep tier needs 8 of each.

A good negative is a near miss: a request in the same area that the skill must not take. "Explain what a mutex is" is a weak negative for a code-review skill. "Write a function that parses dates" is a better one.

## Tiers

| Tier | Evals that can be scored | Should trigger | Should not trigger |
|---|---|---|---|
| smoke | 5 | 0 | 0 |
| full | 10 | 3 | 3 |
| deep | 16 | 8 | 8 |

An eval is void when it has no expectations and no checks, when a file in `files` is missing, or when a check cannot run. A void eval is never scored, so it can drop the suite below a tier. `skael eval check` names each void eval and why.
