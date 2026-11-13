#!/usr/bin/env bash
# Fails when configuration.md or .env.example differs from the setting registry,
# or when Go code reads an env var the registry does not define.
set -euo pipefail
cd "$(dirname "$0")/.."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go run ./cmd/server --config-docs >"$tmp/configuration.md" 2>/dev/null
go run ./cmd/server --env-example >"$tmp/env.example" 2>/dev/null

fail=0
if ! diff -q "$tmp/configuration.md" site/src/content/docs/docs/configuration.md >/dev/null; then
  echo "settings: site/src/content/docs/docs/configuration.md is stale; run: go run ./cmd/server --config-docs > site/src/content/docs/docs/configuration.md"
  fail=1
fi
if ! diff -q "$tmp/env.example" .env.example >/dev/null; then
  echo "settings: .env.example is stale; run: go run ./cmd/server --env-example > .env.example"
  fail=1
fi

# Names a process reads that are not skael settings.
allow="PATH HOME USER USERNAME HOSTNAME EDITOR VISUAL SKAEL_URL SKAEL_KEY WHETSTONE_BASE_TAG WHETSTONE_LLM_TIMEOUT"
read_names=$(grep -rhoE '(Getenv|LookupEnv|look|env)\("[A-Z][A-Z0-9_]+"\)' --include='*.go' --exclude='*_test.go' cmd internal cli \
  | sed -E 's/.*\("([A-Z0-9_]+)"\)/\1/' | sort -u)
for name in $read_names; do
  case " $allow " in *" $name "*) continue ;; esac
  if ! grep -q "^# $name=\|^$name=" "$tmp/env.example"; then
    echo "settings: Go code reads $name, which internal/settings does not define"
    fail=1
  fi
done
exit $fail
