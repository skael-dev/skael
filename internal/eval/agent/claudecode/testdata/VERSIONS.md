# Recorded fixture provenance

Re-record with `./record.sh`. Never hand-edit the .jsonl files.

| Fixture | CLI | Version | Recorded |
|---|---|---|---|
| basic-tools.jsonl | claude | 2.1.220 (Claude Code) | 2026-07-30 |
| skill-invocation.jsonl | claude | 2.1.220 (Claude Code) | 2026-07-30 |

A parser test failing after a CLI upgrade is the intended signal, not a
flake: re-record, diff the fixture, and fix the mapping.
