package hooks

import (
	"os"
	"path/filepath"
)

// hookScript is the content of the skael hook bash script.
// It reads stdin from the agent's hook system, extracts the skill name,
// hashes project path and developer identity for privacy, and POSTs an
// activation event to SKAEL_ENDPOINT/api/events (fire-and-forget).
const hookScript = `#!/usr/bin/env bash
# skael-hook.sh — managed by skael CLI
set -euo pipefail

ENDPOINT="${SKAEL_ENDPOINT:-}"
API_KEY="${SKAEL_API_KEY:-}"
AGENT="${SKAEL_AGENT:-unknown}"

# Nothing to do without an endpoint.
[ -z "$ENDPOINT" ] && exit 0

# Read stdin (agent hook payload).
PAYLOAD="$(cat)"

# Extract skill name: prefer jq, fall back to grep.
if command -v jq >/dev/null 2>&1; then
  SKILL_NAME="$(printf '%s' "$PAYLOAD" | jq -r '.skillName // .skill_name // .tool_name // "" ' 2>/dev/null || true)"
else
  SKILL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"skillName"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  if [ -z "$SKILL_NAME" ]; then
    SKILL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"skill_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  fi
  if [ -z "$SKILL_NAME" ]; then
    SKILL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"tool_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  fi
fi

# Hash project path and developer identity for privacy.
PROJECT_DIR="$(pwd)"
GIT_AUTHOR="$(git config user.email 2>/dev/null || echo "")"
IDENTITY="${GIT_AUTHOR:-$(id -u)}"

PROJECT_HASH="$(printf '%s' "$PROJECT_DIR" | shasum -a 256 | awk '{print $1}')"
DEV_HASH="$(printf '%s' "$IDENTITY"     | shasum -a 256 | awk '{print $1}')"

# Build JSON payload.
EVENT_JSON="$(printf '{"skill_name":"%s","agent":"%s","trigger_type":"auto","project_hash":"%s","developer_hash":"%s"}' \
  "${SKILL_NAME:-unknown}" \
  "$AGENT" \
  "$PROJECT_HASH" \
  "$DEV_HASH")"

# POST event fire-and-forget (background, suppress output).
(
  curl -s -o /dev/null \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
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
