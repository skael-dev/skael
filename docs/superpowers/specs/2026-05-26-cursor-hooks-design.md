# Cursor Hook Support — Design Spec

## Overview

Add Cursor IDE as a supported agent in Skael. Cursor handles skills differently from Claude Code: skills are context-injected, not tool-invoked, so `preToolUse` hooks cannot intercept skill loading. Instead, we use a `stop` hook that fires at session end, parses the transcript JSONL file, extracts which skills were used, and batch-POSTs activation events.

## How Cursor skills work (vs Claude Code)

| | Claude Code | Cursor |
|---|---|---|
| Skill loading | `Skill` tool call (real tool invocation) | Context injection (no tool call) |
| Hook-trackable via preToolUse? | Yes — `matcher: "Skill"` works | No — skills bypass tool pipeline |
| Alternative tracking | N/A (preToolUse works) | `stop` hook + transcript parsing |

## Agent detection

New file `cli/agents/cursor.go`:

```go
type Cursor struct{}
Name()       → "cursor"
SkillsDir()  → ~/.cursor/skills/
ConfigPath() → ~/.cursor/hooks.json
Detected()   → ~/.cursor/ directory exists
```

Registered in `DetectAll`. Skills sync to `~/.cursor/skills/` via `skael sync`.

## Hook installation

Write a `stop` hook entry to `~/.cursor/hooks.json`. The file format follows Cursor's hooks.json spec:

```json
{
  "version": 1,
  "hooks": {
    "stop": [
      {
        "_managed_by": "skael",
        "command": "SKAEL_AGENT=cursor ~/.skael/hooks/skael-cursor-stop.sh"
      }
    ]
  }
}
```

Installation follows the same pattern as Claude Code: read existing file (or start with empty object), merge the skael hook entry, write back. Uses the `_managed_by: skael` marker for idempotent install/uninstall.

If `hooks.json` already has other `stop` hooks, the skael entry is appended to the array, not replacing existing hooks.

## Stop hook script

New script `skael-cursor-stop.sh` written alongside the existing `skael-hook.sh` by `WriteHookScript`.

### Input

The `stop` hook receives JSON on stdin:

```json
{
  "session_id": "abc123",
  "transcript_path": "/path/to/transcript.jsonl",
  "cwd": "/current/working/dir"
}
```

### Processing

1. Read `transcript_path` from stdin JSON
2. Read the JSONL transcript file
3. Parse each line with jq, looking for skill references:
   - Lines where skill content was loaded (look for SKILL.md frontmatter patterns like `name:` fields in context injections)
   - Lines with `tool_input.skill` or `tool_input.skill_name` fields
   - Lines referencing `.cursor/skills/` paths
4. Extract unique skill names
5. For each skill name, POST an activation event to `/api/events` with `agent: "cursor"`

### Script structure

```bash
#!/usr/bin/env bash
set -euo pipefail

# Read config
CONFIG_FILE="${HOME}/.skael/config.json"
if [ ! -f "$CONFIG_FILE" ]; then exit 0; fi
ENDPOINT=$(jq -r '.endpoint // empty' "$CONFIG_FILE")
API_KEY=$(jq -r '.api_key // empty' "$CONFIG_FILE")
if [ -z "$ENDPOINT" ] || [ -z "$API_KEY" ]; then exit 0; fi

# Read transcript path from stdin
INPUT="$(cat)"
TRANSCRIPT_PATH=$(printf '%s' "$INPUT" | jq -r '.transcript_path // empty')
if [ -z "$TRANSCRIPT_PATH" ] || [ ! -f "$TRANSCRIPT_PATH" ]; then exit 0; fi

# Extract skill names from transcript
# Look for skill activations: lines containing skill content with frontmatter name fields
SKILL_NAMES=$(jq -r '
  select(.type == "tool_use" or .type == "assistant" or .role == "assistant")
  | .. | strings
  | capture("skills/(?<name>[a-z0-9][a-z0-9:.-]*[a-z0-9])/SKILL\\.md") // empty
  | .name
' "$TRANSCRIPT_PATH" 2>/dev/null | sort -u)

# Hash project and developer for privacy
HASH_CMD="shasum -a 256"
command -v sha256sum &>/dev/null && HASH_CMD="sha256sum"
CWD=$(printf '%s' "$INPUT" | jq -r '.cwd // empty')
PROJECT_HASH=$(printf '%s' "${CWD:-unknown}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)
DEV_HASH=$(printf '%s' "${USER:-unknown}@${HOSTNAME:-unknown}" | $HASH_CMD | cut -d' ' -f1 | head -c 16)

# POST one event per skill (fire-and-forget)
for SKILL in $SKILL_NAMES; do
  EVENT=$(jq -n --arg sn "$SKILL" --arg ag "cursor" --arg tt "auto" \
    --arg ph "$PROJECT_HASH" --arg dh "$DEV_HASH" \
    '{skill_name:$sn,agent:$ag,trigger_type:$tt,project_hash:$ph,developer_hash:$dh}')
  curl -sf -X POST -H "Content-Type: application/json" -H "X-API-Key: $API_KEY" \
    -d "$EVENT" "${ENDPOINT}/api/events" &>/dev/null &
done
disown 2>/dev/null || true
exit 0
```

The jq transcript parsing looks for path references to `skills/<name>/SKILL.md` in the transcript content. This catches skill activations regardless of how Cursor internally represents them, since the skill file path appears in the transcript when content is loaded.

## Hook installation logic

Add `installCursorHook` and `uninstallCursorHook` to `cli/hooks/install.go`. The JSON format is similar to Claude Code's `settings.json` but with a different structure:

- Claude Code: `settings.hooks.PreToolUse[].hooks[]`
- Cursor: `hooks.stop[]`

The install function:
1. Read `~/.cursor/hooks.json` (or start with `{"version": 1, "hooks": {}}`)
2. Find or create the `hooks.stop` array
3. Check if a `_managed_by: skael` entry exists — update or append
4. Write back

Uninstall: remove any entry with `_managed_by: skael` from the `stop` array.

## InstallForAgent routing

Update `InstallForAgent` in `install.go` to handle the `"cursor"` agent name, routing to `installCursorHook`.

## Out of scope

- Real-time activation tracking (Cursor doesn't support it)
- MCP server approach (deferred to future)
- Cursor plugin marketplace distribution (separate concern)
- Gemini CLI support (separate spec)
