#!/usr/bin/env bash
# check: agents-md
# born: 2026-09-13
# failure: AGENTS.md grew past its budget and kept commands and paths that no longer exist, and the agent trusted them
# rule: every AGENTS.md is within 60 lines, every quoted path exists, every quoted command resolves, and a sibling CLAUDE.md imports it
# Usage: agents-md.sh         fast: budget, paths, binaries, just recipes, package scripts, CLAUDE.md import
#        agents-md.sh --run   slow: also runs each command whose every step is read-only; lists what it skipped
# It does not judge content, tone or completeness.
set -euo pipefail

limit=60
run=0
[[ "${1:-}" == "--run" ]] && run=1
root="$(git rev-parse --show-toplevel)"
cd "$root"
fail=0
skipped=()

err() { echo "rule: $1" >&2; fail=1; }

# Emit "<line>\t<kind>\t<text>" for each quoted item. kind: cmd (fenced shell line or multi-word inline span), one (single-word inline span).
extract() {
  awk '
    /^[[:space:]]*(```|~~~)/ {
      if (!infence) { infence=1; lang=$0; sub(/^[[:space:]]*(```|~~~)[[:space:]]*/, "", lang); shell=(lang=="" || lang ~ /^(sh|bash|shell|zsh|console)$/); next }
      infence=0; next
    }
    infence { if (shell) { l=$0; sub(/^[[:space:]]*\$?[[:space:]]*/, "", l); if (l != "" && l !~ /^#/) printf "%d\tcmd\t%s\n", NR, l } ; next }
    {
      s=$0
      while (match(s, /`[^`]+`/)) {
        t=substr(s, RSTART+1, RLENGTH-2); s=substr(s, RSTART+RLENGTH)
        printf "%d\t%s\t%s\n", NR, (t ~ /[[:space:]]/ ? "cmd" : "one"), t
      }
    }' "$1"
}

is_path() { # single token that names a file or directory
  local t="$1"
  [[ "$t" == *"://"* || "$t" == *"<"* || "$t" == *"*"* || "$t" == *'$'* || "$t" == *"{"* || "$t" == @* ]] && return 1
  [[ "$t" == */* ]] && return 0
  [[ "$t" =~ ^[A-Za-z0-9_.-]*\.[a-z]{1,5}$ && "$t" =~ [a-z] ]] && return 0
  return 1
}

# A step is read-only when its binary and subcommand never write outside caches or deploy.
read_only() {
  local w; read -r -a w <<< "$1"
  case "${w[0]:-}" in
    ls|cat|grep|rg|head|tail|wc|jq|echo|true|test|which|pwd|env|diff) return 0 ;;
    git) [[ "${w[1]:-}" =~ ^(status|log|diff|show|ls-files|rev-parse)$ ]] ;;
    go) [[ "${w[1]:-}" =~ ^(vet|test|list|version|env)$ ]] ;;
    gofmt) [[ "${w[1]:-}" =~ ^-(l|d)$ ]] ;;
    golangci-lint) [[ "${w[1]:-}" == run && "$1" != *--fix* ]] ;;
    cargo) [[ "${w[1]:-}" =~ ^(check|test|clippy)$ && "$1" != *--fix* ]] ;;
    tsc) [[ "$1" == *--noEmit* ]] ;;
    just) [[ "${w[1]:-}" =~ ^--(list|summary|show)$ ]] ;;
    *) return 1 ;;
  esac
}

just_recipes=""
if command -v just > /dev/null && [[ -f justfile || -f Justfile || -f .justfile ]]; then
  just_recipes="$(just --summary 2>/dev/null | tr ' ' '\n')"
fi

check_cmd() { # check_cmd <file> <line> <dir> <command>
  local f="$1" n="$2" dir="$3" c="$4" w bin
  c="${c#sudo }"
  while [[ "$c" =~ ^[A-Z_][A-Z0-9_]*=[^[:space:]]*[[:space:]]+ ]]; do c="${c#"${BASH_REMATCH[0]}"}"; done
  read -r -a w <<< "$c"
  bin="${w[0]:-}"
  [[ -z "$bin" || "$bin" == *"<"* ]] && return
  if [[ "$bin" == ./* || "$bin" == */* ]]; then
    [[ -e "$dir/$bin" ]] || { err "$f:$n: command not found: $bin"; return; }
  elif ! command -v "$bin" > /dev/null; then
    err "$f:$n: command not found: $bin"; return
  fi
  if [[ "$bin" == just && -n "${w[1]:-}" && "${w[1]}" != -* ]]; then
    grep -qxF "${w[1]}" <<< "$just_recipes" || { err "$f:$n: just recipe not found: ${w[1]}"; return; }
  fi
  if [[ "$bin" =~ ^(bun|npm|pnpm|yarn)$ && "${w[1]:-}" == run && -n "${w[2]:-}" ]]; then
    jq -e --arg s "${w[2]}" '.scripts[$s]' "$dir/package.json" > /dev/null 2>&1 || { err "$f:$n: package script not found: ${w[2]}"; return; }
  fi
  (( run )) || return 0
  local steps
  if [[ "$bin" == just && -n "${w[1]:-}" && "${w[1]}" != -* ]]; then
    steps="$(just --dry-run "${w[@]:1}" 2>&1 >/dev/null)" || { err "$f:$n: just --dry-run failed: $c"; return; }
    grep -q '^#!' <<< "$steps" && { skipped+=("$f:$n: $c (script recipe)"); return; }
  else
    steps="$c"
  fi
  while IFS= read -r s; do
    [[ -z "$s" || "$s" == \#* ]] && continue
    read_only "$s" || { skipped+=("$f:$n: $c (step: $s)"); return; }
  done <<< "$(tr ';&|' '\n\n\n' <<< "$steps")"
  (cd "$dir" && bash -c "$c" > /dev/null 2>&1) || err "$f:$n: command failed: $c"
}

while IFS= read -r f; do
  dir="$(dirname "$f")"
  lines=$(wc -l < "$f" | tr -d ' ')
  (( lines <= limit )) || err "$f has $lines lines, limit $limit. Delete a line or convert it to a check."
  if [[ -f "$dir/CLAUDE.md" ]] && ! grep -qxF '@AGENTS.md' "$dir/CLAUDE.md"; then
    err "$dir/CLAUDE.md does not import AGENTS.md. Add the line @AGENTS.md."
  fi
  while IFS=$'\t' read -r n kind text; do
    if [[ "$kind" == one ]]; then
      is_path "$text" || continue
      p="${text/#\~/$HOME}"
      [[ "$p" == /* ]] || p="$dir/$p"
      [[ -e "$p" ]] || err "$f:$n: path does not exist: $text"
    else
      check_cmd "$f" "$n" "$dir" "$text"
    fi
  done < <(extract "$f")
done < <(git ls-files --cached --others --exclude-standard -- 'AGENTS.md' '**/AGENTS.md')

if (( ${#skipped[@]} )); then
  echo "agents-md: not run, not provably read-only:" >&2
  printf '  %s\n' "${skipped[@]}" >&2
fi
exit "$fail"
