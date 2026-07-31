#!/usr/bin/env bash
# Regenerates the recorded stream fixtures from the real Claude Code CLI.
#
# These fixtures are ground truth for the parser contract tests. Do not
# hand-edit them: if the CLI's format changes, the correct response is to
# re-record and fix the parser, which is the whole point of recording.
#
# Requires: an authenticated `claude` on PATH. Not run in CI.
set -euo pipefail

cd "$(dirname "$0")"
out="$PWD"
ws="$(mktemp -d)"
trap 'rm -rf "$ws"' EXIT

version="$(claude --version 2>/dev/null | head -1)"

record() {
  local name="$1" prompt="$2"
  echo "recording $name..." >&2
  ( cd "$ws" && rm -rf ./* 2>/dev/null || true
    claude -p "$prompt" \
      --output-format stream-json \
      --verbose \
      --permission-mode acceptEdits \
      < /dev/null
  ) > "$out/$name.jsonl"
}

record basic-tools      "Create a file named hello.txt containing the word hi, then read it back."
record skill-invocation "Use the find-skills skill to look for a skill about PDFs. Do not do anything else."

cat > "$out/VERSIONS.md" <<EOF
# Recorded fixture provenance

Re-record with \`./record.sh\`. Never hand-edit the .jsonl files.

| Fixture | CLI | Version | Recorded |
|---|---|---|---|
| basic-tools.jsonl | claude | $version | $(date -u +%Y-%m-%d) |
| skill-invocation.jsonl | claude | $version | $(date -u +%Y-%m-%d) |

A parser test failing after a CLI upgrade is the intended signal, not a
flake: re-record, diff the fixture, and fix the mapping.
EOF

echo "done — fixtures in $out" >&2
