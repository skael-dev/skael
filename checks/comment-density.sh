#!/usr/bin/env bash
# Fails when a file in the diff adds more comment than a reader can use.
#
# The prose rule ("comment the non-obvious decision") is not enforceable and has
# been ignored. The ratio is. 25% is where deleting stopped removing slop and
# started removing meaning, measured by trimming the densest files in the repo:
# a file that trips this is not slightly over, it is prose with code in it.
set -euo pipefail

BASE="${1:-}"
HEAD_REF="${2:-HEAD}"
if [ -z "$BASE" ]; then
  BASE=$(git merge-base "$HEAD_REF" main 2>/dev/null || echo "")
fi
# A shallow clone has no main to diff against. Skipping is right: this is a
# check for the author, run before the work leaves the machine.
if [ -z "$BASE" ]; then
  echo "comment-density: no base to compare against; skipping"
  exit 0
fi

MAX_PERCENT="${COMMENT_DENSITY_MAX:-25}"
MIN_ADDED=25
REPORT=$(mktemp)
trap 'rm -f "$REPORT"' EXIT

git diff "$BASE"..."$HEAD_REF" --name-only --diff-filter=d \
  -- '*.go' '*.ts' '*.tsx' '*.css' '*.sql' \
| while read -r file; do
    [ -n "$file" ] || continue

    added=$(git diff "$BASE"..."$HEAD_REF" -- "$file" | grep -c '^+[^+]' || true)
    [ "$added" -ge "$MIN_ADDED" ] || continue

    comments=$(git diff "$BASE"..."$HEAD_REF" -- "$file" \
      | grep '^+[^+]' \
      | sed 's/^+[[:space:]]*//' \
      | grep -c -E '^(//|/\*|\*|\{/\*|--( |$))' || true)

    percent=$(( comments * 100 / added ))
    if [ "$percent" -gt "$MAX_PERCENT" ]; then
      echo "$file: $percent% of $added added lines are comments (max ${MAX_PERCENT}%)"
    fi
  done > "$REPORT" || true

if [ -s "$REPORT" ]; then
  echo "comment-density: too much prose in these files:"
  cat "$REPORT"
  echo
  echo "Keep the decision a reader cannot infer. Delete the rest."
  echo "A file that genuinely needs it: COMMENT_DENSITY_MAX=<n> just check"
  exit 1
fi
