# Recorded fixture provenance

| Fixture | CLI | Version | Recorded |
|---|---|---|---|
| opencode-session.jsonl | opencode | 1.19.5 | 2026-11-10 |

Recorded with `opencode run --format json --auto` against OpenRouter, with a
skill installed. Never hand-edit it. A parser test failing after an OpenCode
upgrade is the intended signal: record a new session, diff it, and fix the
mapping.
