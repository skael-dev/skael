package hooks

import (
	"os"
	"path/filepath"
)

// cursorScript is Cursor's postToolUse hook on Read. Cursor has no skill tool:
// it loads a skill by reading the skill's SKILL.md, so one read of a SKILL.md
// directly inside a skills folder Cursor loads from is one activation. A
// SKILL.md anywhere else, such as a skill repo's working copy, is a file read.
const cursorScript = `#!/usr/bin/env bash
# skael-cursor-read.sh — managed by skael CLI
# Cursor postToolUse hook (matcher Read). Reports a read of
# <dir>/{.agents,.cursor,.claude,.codex}/skills/<skill>/SKILL.md as an activation.
set -uo pipefail

INPUT="$(cat)"

CONFIG_FILE="${HOME}/.skael/config.json"
if [ ! -f "$CONFIG_FILE" ]; then exit 0; fi
if ! command -v jq &>/dev/null; then exit 0; fi

ENDPOINT=$(jq -r '.endpoint // empty' "$CONFIG_FILE" 2>/dev/null || true)
API_KEY=$(jq -r '.api_key // empty' "$CONFIG_FILE" 2>/dev/null || true)
if [ -z "$ENDPOINT" ] || [ -z "$API_KEY" ]; then exit 0; fi

if [ "$(printf '%s' "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)" != "Read" ]; then exit 0; fi
FILE=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // .tool_input.path // empty' 2>/dev/null || true)
case "$FILE" in */SKILL.md) ;; *) exit 0 ;; esac
SKILL_DIR=$(dirname "$FILE")
FOLDER=$(basename "$SKILL_DIR")
case "$(dirname "$SKILL_DIR")" in
  */.agents/skills|*/.cursor/skills|*/.claude/skills|*/.codex/skills) ;;
  *) exit 0 ;;
esac

# A skill skael installed sits in a folder named exactly its canonical name.
# Any other skill is named by its frontmatter, then by its folder.
STATE_FILE="${HOME}/.skael/state.json"
in_state() {
  [ -f "$STATE_FILE" ] && jq -e --arg n "$1" 'any(.skills[]?; .name == $n)' "$STATE_FILE" &>/dev/null
}
SKILL="$FOLDER"
if ! in_state "$FOLDER" && [ -f "$FILE" ]; then
  FM=$(awk 'NR==1 { if ($0 !~ /^---\r?$/) exit; next } /^---\r?$/ { exit } /^name:/ { sub(/^name:[ \t]*/, ""); sub(/\r$/, ""); gsub(/^["'"'"']|["'"'"'][ \t]*$/, ""); print; exit }' "$FILE" 2>/dev/null || true)
  if [ -n "$FM" ]; then SKILL="$FM"; fi
fi
if [ "${#SKILL}" -gt 128 ]; then exit 0; fi
if ! printf '%s' "$SKILL" | grep -Eq '^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$'; then exit 0; fi

VERSION=0
if [ -f "$STATE_FILE" ]; then
  VERSION=$(jq -r --arg n "$SKILL" '[.skills[]? | select(.name == $n) | .version][0] // 0' "$STATE_FILE" 2>/dev/null || echo 0)
fi
case "$VERSION" in ''|*[!0-9]*) VERSION=0 ;; esac

if command -v sha256sum &>/dev/null; then
  HASH_CMD="sha256sum"
elif command -v shasum &>/dev/null; then
  HASH_CMD="shasum -a 256"
else
  HASH_CMD=""
fi

# Cursor runs a user hook from ~/.cursor, so the project is the first workspace root.
PROJECT=$(printf '%s' "$INPUT" | jq -r '.workspace_roots[0] // .cwd // empty' 2>/dev/null || true)
if [ -n "$HASH_CMD" ]; then
  PROJECT_HASH=$(printf '%s' "${PROJECT:-$PWD}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)
  DEV_HASH=$(printf '%s' "${USER:-unknown}@${HOSTNAME:-unknown}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)
else
  PROJECT_HASH="nohash"
  DEV_HASH="nohash"
fi

# "default" is Cursor's automatic choice, not a model.
MODEL=$(printf '%s' "$INPUT" | jq -r '.model_id // .model // empty' 2>/dev/null || true)
if [ "$MODEL" = "default" ] || ! printf '%s' "$MODEL" | grep -Eq '^[A-Za-z0-9._:/@-]{1,128}$'; then MODEL=""; fi

EVENT=$(jq -n \
  --arg sn "$SKILL" \
  --arg ag "cursor" \
  --arg tt "auto" \
  --arg ph "$PROJECT_HASH" \
  --arg dh "$DEV_HASH" \
  --arg es "tool_invocation" \
  --arg md "$MODEL" \
  --argjson sv "$VERSION" \
  '{skill_name:$sn,agent:$ag,trigger_type:$tt,project_hash:$ph,developer_hash:$dh,event_source:$es,model:$md,version:$sv}')
curl -sf -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d "$EVENT" \
  "${ENDPOINT}/api/events" &>/dev/null &
disown 2>/dev/null || true

exit 0
`

// WriteCursorScript writes Cursor's activation hook script and returns its path.
func WriteCursorScript(skaelDir string) (string, error) {
	hooksDir := filepath.Join(skaelDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}
	// The stop hook script before skael tracked Cursor reads. Its hooks.json
	// entry is removed on install, so the file is unused.
	_ = os.Remove(filepath.Join(hooksDir, "skael-cursor-stop.sh"))

	scriptPath := filepath.Join(hooksDir, "skael-cursor-read.sh")
	if err := os.WriteFile(scriptPath, []byte(cursorScript), 0o755); err != nil {
		return "", err
	}
	return scriptPath, nil
}
