package hooks

import (
	"os"
	"path/filepath"
)

const cursorStopScript = `#!/usr/bin/env bash
# skael-cursor-stop.sh — managed by skael CLI
# Fires at session end. Parses transcript for skill activations.
set -euo pipefail

CONFIG_FILE="${HOME}/.skael/config.json"
if [ ! -f "$CONFIG_FILE" ]; then exit 0; fi

if ! command -v jq &>/dev/null; then exit 0; fi

ENDPOINT=$(jq -r '.endpoint // empty' "$CONFIG_FILE")
API_KEY=$(jq -r '.api_key // empty' "$CONFIG_FILE")
if [ -z "$ENDPOINT" ] || [ -z "$API_KEY" ]; then exit 0; fi

INPUT="$(cat)"
TRANSCRIPT_PATH=$(printf '%s' "$INPUT" | jq -r '.transcript_path // empty')
if [ -z "$TRANSCRIPT_PATH" ] || [ ! -f "$TRANSCRIPT_PATH" ]; then exit 0; fi

SKILL_NAMES=$(jq -r '
  .. | strings
  | match("skills/([a-z0-9][a-z0-9:._-]*[a-z0-9])/SKILL\\.md"; "g")
  | .captures[0].string
' "$TRANSCRIPT_PATH" 2>/dev/null | sort -u || true)

if [ -z "$SKILL_NAMES" ]; then exit 0; fi

if command -v sha256sum &>/dev/null; then
  HASH_CMD="sha256sum"
elif command -v shasum &>/dev/null; then
  HASH_CMD="shasum -a 256"
else
  HASH_CMD=""
fi

CWD=$(printf '%s' "$INPUT" | jq -r '.cwd // empty')
if [ -n "$HASH_CMD" ]; then
  PROJECT_HASH=$(printf '%s' "${CWD:-unknown}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)
  DEV_HASH=$(printf '%s' "${USER:-unknown}@${HOSTNAME:-unknown}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)
else
  PROJECT_HASH="nohash"
  DEV_HASH="nohash"
fi

for SKILL in $SKILL_NAMES; do
  EVENT=$(jq -n \
    --arg sn "$SKILL" \
    --arg ag "cursor" \
    --arg tt "auto" \
    --arg ph "$PROJECT_HASH" \
    --arg dh "$DEV_HASH" \
    '{skill_name:$sn,agent:$ag,trigger_type:$tt,project_hash:$ph,developer_hash:$dh}')
  curl -sf -X POST \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "$EVENT" \
    "${ENDPOINT}/api/events" &>/dev/null &
done
disown 2>/dev/null || true

exit 0
`

func WriteCursorStopScript(skaalDir string) (string, error) {
	hooksDir := filepath.Join(skaalDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(hooksDir, "skael-cursor-stop.sh")
	if err := os.WriteFile(scriptPath, []byte(cursorStopScript), 0o755); err != nil {
		return "", err
	}

	return scriptPath, nil
}
