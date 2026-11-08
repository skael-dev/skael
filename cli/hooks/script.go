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

field() {
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$PAYLOAD" | jq -r --arg k "$1" '.[$k] // ""' 2>/dev/null || true
  else
    printf '%s' "$PAYLOAD" | grep -o "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true
  fi
}

# A session start or model switch records the session's model for the
# activations that follow, then exits.
SESSION_ID="$(field session_id)"
case "$SESSION_ID" in *[!A-Za-z0-9_-]*) SESSION_ID="" ;; esac
SESSIONS_DIR="${HOME}/.skael/sessions"
case "$(field hook_event_name)" in
  SessionStart|PostModelSwitch)
    SESSION_MODEL="$(field to_model)"
    [ -z "$SESSION_MODEL" ] && SESSION_MODEL="$(field model)"
    if [ -n "$SESSION_ID" ] && [ -n "$SESSION_MODEL" ]; then
      mkdir -p "$SESSIONS_DIR" && printf '%s' "$SESSION_MODEL" > "$SESSIONS_DIR/$SESSION_ID"
    fi
    find "$SESSIONS_DIR" -type f -mtime +7 -delete 2>/dev/null || true
    exit 0
    ;;
esac

# Identify the invoking tool. Codex has no matcher mechanism, so every tool call
# reaches this script and the filter has to live here. An empty tool name means
# an agent whose payload shape we do not recognise — fall through and let the
# skill-name checks below decide.
if command -v jq >/dev/null 2>&1; then
  TOOL_NAME="$(printf '%s' "$PAYLOAD" | jq -r '.tool_name // .tool // ""' 2>/dev/null || true)"
else
  TOOL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"tool_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  if [ -z "$TOOL_NAME" ]; then
    TOOL_NAME="$(printf '%s' "$PAYLOAD" | grep -o '"tool"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*: *"\(.*\)"/\1/' || true)"
  fi
fi

case "$TOOL_NAME" in
  ""|Skill|skill|skills|skills_*) ;;
  *) exit 0 ;;
esac

# Extract the skill name. Claude Code's Skill tool puts it in .tool_input.skill;
# other agents use skill_name/skillName. There is deliberately no generic
# .tool_input.name fallback — it matches unrelated tools' name parameters and
# records them as activations.
if command -v jq >/dev/null 2>&1; then
  SKILL_NAME="$(printf '%s' "$PAYLOAD" | jq -r '.tool_input.skill // .tool_input.skill_name // .tool_input.skillName // .skill_name // .skillName // ""' 2>/dev/null || true)"
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

# No skill name means this was not a skill invocation. Posting "unknown" here
# would corrupt the activation data the product is built on.
if [ -z "$SKILL_NAME" ] || [ "$SKILL_NAME" = "null" ]; then exit 0; fi

# Same shape and length the registry enforces. Anything else is not a skill.
if [ "${#SKILL_NAME}" -gt 128 ]; then exit 0; fi
if ! printf '%s' "$SKILL_NAME" | grep -Eq '^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$'; then exit 0; fi

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

# The model comes from the last assistant line of the transcript, else from the
# session start. Unknown is left out, never guessed.
MODEL=""
TRANSCRIPT="$(field transcript_path)"
if [ -n "$TRANSCRIPT" ] && [ -f "$TRANSCRIPT" ]; then
  MODEL="$(tail -n 400 "$TRANSCRIPT" 2>/dev/null | grep -o '"model":"[^"]*"' | tail -1 | sed 's/"model":"//; s/"$//' || true)"
fi
if [ -z "$MODEL" ] && [ -n "$SESSION_ID" ] && [ -f "$SESSIONS_DIR/$SESSION_ID" ]; then
  MODEL="$(head -c 128 "$SESSIONS_DIR/$SESSION_ID" 2>/dev/null || true)"
fi
if ! printf '%s' "$MODEL" | grep -Eq '^[A-Za-z0-9._:/@-]{1,128}$'; then MODEL=""; fi

# The installed version comes from the last sync. 0 means unknown.
SKILL_VERSION=0
STATE_FILE="${HOME}/.skael/state.json"
if [ -f "$STATE_FILE" ] && command -v jq >/dev/null 2>&1; then
  SKILL_VERSION="$(jq -r --arg n "$SKILL_NAME" '[.skills[]? | select(.name == $n) | .version][0] // 0' "$STATE_FILE" 2>/dev/null || echo 0)"
fi
case "$SKILL_VERSION" in ''|*[!0-9]*) SKILL_VERSION=0 ;; esac

# The rest runs in the background, so the agent never waits on it.
(
  # The harness version is cached for a day: running the CLI costs time.
  CACHE="${HOME}/.skael/cache/harness-${AGENT}"
  HARNESS_VERSION=""
  if [ -f "$CACHE" ] && [ -z "$(find "$CACHE" -mtime +0 2>/dev/null)" ]; then
    HARNESS_VERSION="$(head -c 32 "$CACHE")"
  else
    case "$AGENT" in
      claude-code) BIN=claude ;;
      codex) BIN=codex ;;
      opencode) BIN=opencode ;;
      *) BIN="" ;;
    esac
    if [ -n "$BIN" ] && command -v "$BIN" >/dev/null 2>&1; then
      HARNESS_VERSION="$("$BIN" --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
      mkdir -p "$(dirname "$CACHE")" && printf '%s' "$HARNESS_VERSION" > "$CACHE"
    fi
  fi
  case "$HARNESS_VERSION" in *[!0-9.]*) HARNESS_VERSION="" ;; esac

  if command -v jq &>/dev/null; then
    EVENT_JSON="$(jq -n \
      --arg sn "$SKILL_NAME" \
      --arg ag "$AGENT" \
      --arg ph "$PROJECT_HASH" \
      --arg dh "$DEV_HASH" \
      --arg md "$MODEL" \
      --arg hv "$HARNESS_VERSION" \
      --argjson sv "$SKILL_VERSION" \
      '{skill_name:$sn,agent:$ag,trigger_type:"auto",project_hash:$ph,developer_hash:$dh,event_source:"tool_invocation",model:$md,harness_version:$hv,version:$sv}')"
  else
    SKILL_NAME_ESCAPED="$(printf '%s' "$SKILL_NAME" | sed 's/"/\\"/g')"
    EVENT_JSON="$(printf '{"skill_name":"%s","agent":"%s","trigger_type":"auto","project_hash":"%s","developer_hash":"%s","event_source":"tool_invocation","model":"%s","harness_version":"%s","version":%s}' \
      "$SKILL_NAME_ESCAPED" "$AGENT" "$PROJECT_HASH" "$DEV_HASH" "$MODEL" "$HARNESS_VERSION" "$SKILL_VERSION")"
  fi

  curl -s -o /dev/null \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "$EVENT_JSON" \
    "${ENDPOINT}/api/events" \
  &>/dev/null
) </dev/null &>/dev/null &
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
