---
title: Core concepts
description: The handful of ideas that make up skael.
---

## Skill

A directory containing a `SKILL.md` (YAML frontmatter + markdown) and optional `scripts/`, `references/`, and `assets/`. Skill names match `^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$` (colons allow namespaces, e.g. `superpowers:brainstorming`).

## Version

Every publish creates a new immutable version (sequential integers — `1`, `2`, `3`, not semver). Archives are content-addressable, so concurrent publishes never clobber each other. You can always see and roll back to which version your team is on.

## Sync

`skael sync` diffs a manifest (`{name, version, checksum}`) against what's installed and downloads only what changed, placing each skill into every detected agent's skills directory. Downloaded archives are checksum-verified before extraction.

## Scanning

Every publish (and every import) runs a security scan for hardcoded secrets, prompt injection, data exfiltration, dangerous shell commands, and obfuscation. Critical and high-severity findings block the publish.

## Activations

Lightweight hooks installed in each agent report an event whenever a skill fires — skill name, agent, trigger type, and privacy-preserving hashed project/developer identifiers. That's how skael answers "which skills are actually used, by which agent, how often."
