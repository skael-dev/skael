package hooks

import (
	"os"
	"path/filepath"
)

// hookScript is the content of the skael hook bash script.
// It reads stdin from the agent's hook system, extracts the skill name,
// hashes project path and developer identity for privacy, and POSTs an
// activation event to SKAEL_ENDPOINT/api/events (fire-and-forget).
// Credentials are read from ~/.skael/config.json — never passed via env vars.
const hookScript = `#!/usr/bin/env bash
# skael-hook.sh — managed by skael CLI
set -euo pipefail

AGENT="${SKAEL_AGENT:-unknown}"

# Read config from file (no credentials in agent config).
CONFIG_FILE="${HOME}/.skael/config.json"
if [ ! -f "$CONFIG_FILE" ]; then exit 0; fi

# Parse config: prefer jq, fall back to grep.
if command -v jq &>/dev/null; then
  ENDPOINT=$(jq -r '.endpoint // empty' "$CONFIG_FILE")
  API_KEY=$(jq -r '.api_key // empty' "$CONFIG_FILE")
else
  ENDPOINT=$(grep -o '"endpoint"[^"]*"[^"]*"' "$CONFIG_FILE" | head -1 | sed 's/.*: *"//' | sed 's/"//')
  API_KEY=$(grep -o '"api_key"[^"]*"[^"]*"' "$CONFIG_FILE" | head -1 | sed 's/.*: *"//' | sed 's/"//')
fi

if [ -z "$ENDPOINT" ] || [ -z "$API_KEY" ]; then exit 0; fi

# Read stdin (agent hook payload).
PAYLOAD="$(cat)"

# Extract skill name from tool_input (where agents put skill parameters).
# Claude Code Skill tool: .tool_input.skill
# Fallback chain covers other agents and payload formats.
if command -v jq >/dev/null 2>&1; then
  SKILL_NAME="$(printf '%s' "$PAYLOAD" | jq -r '.tool_input.skill // .tool_input.skill_name // .tool_input.name // .skill_name // .skillName // "" ' 2>/dev/null || true)"
else
  SKILL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"skill"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  if [ -z "$SKILL_NAME" ]; then
    SKILL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"skill_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  fi
  if [ -z "$SKILL_NAME" ]; then
    SKILL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"skillName"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  fi
fi

# Normalize OpenCode prefix.
SKILL_NAME="${SKILL_NAME#skills_}"

# Cross-platform hash: try sha256sum (Linux) then shasum (macOS), fall back to nohash.
if command -v sha256sum &>/dev/null; then
  HASH_CMD="sha256sum"
elif command -v shasum &>/dev/null; then
  HASH_CMD="shasum -a 256"
else
  HASH_CMD=""
fi

if [ -n "$HASH_CMD" ]; then
  PROJECT_HASH=$(echo -n "${PWD}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)
  DEV_HASH=$(echo -n "${USER:-unknown}@${HOSTNAME:-unknown}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)
else
  PROJECT_HASH="nohash"
  DEV_HASH="nohash"
fi

# Build JSON payload — use jq if available to handle arbitrary skill names safely.
if command -v jq &>/dev/null; then
  EVENT_JSON="$(jq -n \
    --arg sn "${SKILL_NAME:-unknown}" \
    --arg ag "$AGENT" \
    --arg tt "auto" \
    --arg ph "$PROJECT_HASH" \
    --arg dh "$DEV_HASH" \
    '{skill_name:$sn,agent:$ag,trigger_type:$tt,project_hash:$ph,developer_hash:$dh}')"
else
  # Escape double quotes so the JSON is not malformed.
  SKILL_NAME_ESCAPED="$(printf '%s' "${SKILL_NAME:-unknown}" | sed 's/"/\\"/g')"
  EVENT_JSON="$(printf '{"skill_name":"%s","agent":"%s","trigger_type":"auto","project_hash":"%s","developer_hash":"%s"}' \
    "$SKILL_NAME_ESCAPED" \
    "$AGENT" \
    "$PROJECT_HASH" \
    "$DEV_HASH")"
fi

# POST event fire-and-forget (background, suppress output).
(
  curl -s -o /dev/null \
    -X POST \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "$EVENT_JSON" \
    "${ENDPOINT}/api/events" \
  &>/dev/null
) &
disown 2>/dev/null || true

exit 0
`

// WriteHookScript creates ~/.skael/hooks/ and writes skael-hook.sh with 0755 permissions.
// It returns the full path to the written script.
func WriteHookScript(skaalDir string) (string, error) {
	hooksDir := filepath.Join(skaalDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(hooksDir, "skael-hook.sh")
	if err := os.WriteFile(scriptPath, []byte(hookScript), 0o755); err != nil {
		return "", err
	}

	return scriptPath, nil
}
