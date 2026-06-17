package hooks

import (
	"os"
	"path/filepath"
)

const autoSyncScript = `#!/usr/bin/env bash
# skael-autosync.sh — managed by skael CLI
# Debounced auto-sync: runs skael sync --quiet if the last sync was >30 min ago.
# Designed to be called from agent hooks (UserPromptSubmit, sessionStart, etc.).
# Always exits 0 — never blocks the agent.
set -euo pipefail

# Consume stdin (some hook events pipe payload data).
cat > /dev/null 2>&1 || true

# Bail out if skael isn't installed.
if ! command -v skael &>/dev/null; then exit 0; fi

STATE_FILE="${HOME}/.skael/state.json"

# If no state file, we've never synced — go ahead.
if [ ! -f "$STATE_FILE" ]; then
  skael sync --quiet &>/dev/null &
  disown 2>/dev/null || true
  exit 0
fi

# Extract last_sync timestamp.
LAST_SYNC=""
if command -v jq &>/dev/null; then
  LAST_SYNC=$(jq -r '.last_sync // empty' "$STATE_FILE" 2>/dev/null || true)
else
  LAST_SYNC=$(grep -o '"last_sync"[[:space:]]*:[[:space:]]*"[^"]*"' "$STATE_FILE" 2>/dev/null | head -1 | sed 's/.*: *"//' | sed 's/"//' || true)
fi

# If no last_sync recorded, sync now.
if [ -z "$LAST_SYNC" ]; then
  skael sync --quiet &>/dev/null &
  disown 2>/dev/null || true
  exit 0
fi

# Compare timestamps. Use date to convert to epoch seconds.
# macOS date uses -jf, Linux date uses -d.
NOW=$(date +%s 2>/dev/null || echo 0)
if date -jf "%Y-%m-%dT%H:%M:%SZ" "$LAST_SYNC" +%s &>/dev/null 2>&1; then
  LAST_EPOCH=$(date -jf "%Y-%m-%dT%H:%M:%SZ" "$LAST_SYNC" +%s 2>/dev/null || echo 0)
elif date -d "$LAST_SYNC" +%s &>/dev/null 2>&1; then
  LAST_EPOCH=$(date -d "$LAST_SYNC" +%s 2>/dev/null || echo 0)
else
  # Can't parse timestamp — sync to be safe.
  LAST_EPOCH=0
fi

ELAPSED=$(( NOW - LAST_EPOCH ))
THRESHOLD=1800

if [ "$ELAPSED" -ge "$THRESHOLD" ] 2>/dev/null; then
  skael sync --quiet &>/dev/null &
  disown 2>/dev/null || true
fi

exit 0
`

// WriteAutoSyncScript creates ~/.skael/hooks/ and writes skael-autosync.sh with 0755 permissions.
// It returns the full path to the written script.
func WriteAutoSyncScript(skaalDir string) (string, error) {
	hooksDir := filepath.Join(skaalDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(hooksDir, "skael-autosync.sh")
	if err := os.WriteFile(scriptPath, []byte(autoSyncScript), 0o755); err != nil {
		return "", err
	}

	return scriptPath, nil
}
