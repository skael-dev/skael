---
title: CLI reference
description: Every skael command.
---

All commands accept `--json` for scriptable output.

## skael setup `<url> <api-key>`

One-command onboarding: validates the key, writes `~/.skael/config.json`, detects installed agents, runs the first sync, and installs activation-tracking hooks.

## skael sync

Pulls the latest skills from the platform and places them in every detected agent's directory. Only changed skills are downloaded. Supports `--dry-run`.

## skael publish `<dir>`

Validates, security-scans, packs, and uploads a skill directory. Blocked on critical/high findings.

## skael scan `<dir>`

Runs the security scan locally without publishing.

## skael search `<query>`

Full-text search across the registry (with fuzzy matching on names).

## skael list

Lists all skills on the platform.

## skael doctor

Diagnostic health check: config, connectivity, agent detection, hook status.

## skael hook `install` | `status`

Standalone management of the activation-tracking hooks.

## skael import `<source>`

Imports skills from an external source (e.g. a GitHub repository) into your registry, scanning on the way in.
