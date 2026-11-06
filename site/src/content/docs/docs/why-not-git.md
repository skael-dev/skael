---
title: Why not just git?
description: How skael differs from a shared repo or a single vendor's native sharing.
---

## "Can't I just commit `.claude/skills/` to a repo?"

Keep the repo. A git folder gives you a folder. It doesn't place skills into Cursor *and* Codex *and* OpenCode, doesn't sync across machines, doesn't scan for injection, doesn't tell you which version everyone's on, and has no idea which skills your agents actually use.

Connect the repo to skael and git stays the source of truth. Every merge publishes through the scan and the gate, a change made in skael opens a pull request, and every pull request gets a skael check. See [Git sync](/docs/git-sync).

## "Doesn't Claude already do org skill sharing?"

For Claude.ai and Desktop, on Team and Enterprise plans — not Claude Code, and not Cursor, Codex, or OpenCode. skael is vendor-neutral by design: it manages the `SKILL.md` standard across *every* agent your team runs, not one vendor's walled garden.

## What skael adds on top of any of these

- **Cross-agent placement** from one source of truth.
- **Security scanning** on every publish and import.
- **Immutable versioning** with rollback.
- **Activation tracking** — the only way to see which skills actually fire, by which agent, how often.
- **Git sync** that keeps your repo as the source of truth, with a check on every pull request.
