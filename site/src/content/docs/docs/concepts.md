---
title: Core concepts
description: The handful of ideas that make up skael.
---

## Skill

A directory containing a `SKILL.md` (YAML frontmatter + markdown) and optional `scripts/`, `references/`, and `assets/`. Skill names match `^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$` (colons allow namespaces, e.g. `superpowers:brainstorming`).

## Version

Every publish creates a new immutable version (sequential integers — `1`, `2`, `3`, not semver). Archives are content-addressable, so concurrent publishes never clobber each other. You can always see and roll back to which version your team is on.

## Selective sync

Skills are installed explicitly with `skael add`, which tracks them in `~/.skael/config.json` (like `package.json` for skills). `skael sync` only updates skills you've installed — not the full registry. Downloaded archives are checksum-verified before extraction. `skael remove` uninstalls a skill and removes it from your config.

## Auto-sync

A debounced hook script runs `skael sync` automatically in the background. It checks your last sync timestamp and skips if less than 30 minutes old. Installed for Claude Code (`UserPromptSubmit`), Cursor (`sessionStart`), and Codex (`PreToolUse`). Your agents always have the latest versions of your installed skills without manual intervention.

## Scanning

Every publish (and every import) runs a security scan for hardcoded secrets, prompt injection, data exfiltration, dangerous shell commands, and obfuscation. What happens to a finding depends on what it is, not just how severe it's rated:

- Findings that mean a credential or data is **leaving the machine** — a hardcoded secret, a reverse shell — are unappealable. The publish is rejected outright. No version is created, and nothing clears it: not an evaluation, not an admin.
- Everything else that blocks (dangerous shell execution, prompt injection, other heuristic matches) doesn't reject the publish. It **holds** the version instead: the version is created with a real version number and a stored archive, but it isn't served — it's left out of the sync manifest, can't be installed or downloaded, and the skill's "latest version" pointer doesn't move to it.
- A held version clears in one of two ways: a [quality score](/docs/quality) that comes back verified and at or above the configured floor, or an owner/admin running `skael review <name> <version> --approve --reason "..."`.

## Quality score

A number, 0-100, that says whether a skill actually works — not just whether it passed the security scan. It comes from running real tasks with a panel of models, once with the skill and once without, and comparing the results. See [Quality scoring](/docs/quality) for how it's produced and what the different states mean.

## Publish gate

The decision every publish and import goes through after scanning: allow it, allow it with a warning, hold it for review, or block it outright. It's what turns a scan finding into an outcome — see [Scanning](#scanning) above for what each outcome means in practice.

## Activations

Lightweight hooks installed in each agent report an event whenever a skill fires — skill name, agent, trigger type, and privacy-preserving hashed project/developer identifiers. That's how skael answers "which skills are actually used, by which agent, how often."
