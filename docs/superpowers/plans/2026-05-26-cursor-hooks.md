# Cursor Hook Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Cursor IDE as a fourth supported agent with detection, skill syncing, and activation tracking via stop-hook transcript parsing.

**Architecture:** New `Cursor` agent struct follows the existing pattern (Name/SkillsDir/ConfigPath/Detected). A new `skael-cursor-stop.sh` script parses the session transcript JSONL at session end to extract skill names and POST events. Hook installation writes to `~/.cursor/hooks.json` in Cursor's native format.

**Tech Stack:** Go (existing agent/hooks patterns), bash (stop hook script with jq)

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `cli/agents/cursor.go` | Cursor agent detection |
| Modify | `cli/agents/agent.go` | Register Cursor in DetectIn |
| Create | `cli/hooks/cursor_script.go` | Cursor stop-hook bash script |
| Modify | `cli/hooks/install.go` | installCursorHook / uninstallCursorHook + routing |
| Modify | `cli/hooks/script.go` | WriteCursorHookScript function |
| Create | `cli/agents/cursor_test.go` | Detection tests |

---

### Task 1: Cursor agent detection

**Files:**
- Create: `cli/agents/cursor.go`
- Modify: `cli/agents/agent.go`

- [ ] **Step 1: Create the Cursor agent**

```go
// cli/agents/cursor.go
package agents

import "path/filepath"

// Cursor represents the Cursor IDE agent.
type Cursor struct{}

func (c *Cursor) Name() string { return "cursor" }

func (c *Cursor) SkillsDir(home string) string {
	return filepath.Join(home, ".cursor", "skills")
}

func (c *Cursor) ConfigPath(home string) string {
	return filepath.Join(home, ".cursor", "hooks.json")
}

func (c *Cursor) Detected(home string) bool {
	return dirExists(filepath.Join(home, ".cursor"))
}
```

- [ ] **Step 2: Register in DetectIn**

In `cli/agents/agent.go`, add `&Cursor{}` to the `known` slice in `DetectIn`:

```go
known := []Agent{
    &ClaudeCode{},
    &Codex{},
    &OpenCode{},
    &Cursor{},
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git add cli/agents/cursor.go cli/agents/agent.go
git commit -m "feat(cursor): add Cursor agent detection"
```

---

### Task 2: Cursor stop-hook script

**Files:**
- Create: `cli/hooks/cursor_script.go`
- Modify: `cli/hooks/script.go`

- [ ] **Step 1: Create the cursor stop-hook script**

```go
// cli/hooks/cursor_script.go
package hooks

import (
	"os"
	"path/filepath"
)

// cursorStopScript is the content of the skael Cursor stop hook.
// It fires at session end, reads the transcript JSONL, extracts skill
// names, and POSTs activation events for each one.
const cursorStopScript = `#!/usr/bin/env bash
# skael-cursor-stop.sh — managed by skael CLI
# Fires at session end. Parses transcript for skill activations.
set -euo pipefail

# Read config.
CONFIG_FILE="${HOME}/.skael/config.json"
if [ ! -f "$CONFIG_FILE" ]; then exit 0; fi

if ! command -v jq &>/dev/null; then exit 0; fi

ENDPOINT=$(jq -r '.endpoint // empty' "$CONFIG_FILE")
API_KEY=$(jq -r '.api_key // empty' "$CONFIG_FILE")
if [ -z "$ENDPOINT" ] || [ -z "$API_KEY" ]; then exit 0; fi

# Read stop hook input from stdin.
INPUT="$(cat)"
TRANSCRIPT_PATH=$(printf '%s' "$INPUT" | jq -r '.transcript_path // empty')
if [ -z "$TRANSCRIPT_PATH" ] || [ ! -f "$TRANSCRIPT_PATH" ]; then exit 0; fi

# Extract skill names from transcript.
# Look for references to skills/<name>/SKILL.md paths in the transcript content.
SKILL_NAMES=$(jq -r '
  .. | strings
  | match("skills/([a-z0-9][a-z0-9:._-]*[a-z0-9])/SKILL\\.md"; "g")
  | .captures[0].string
' "$TRANSCRIPT_PATH" 2>/dev/null | sort -u || true)

if [ -z "$SKILL_NAMES" ]; then exit 0; fi

# Hash project and developer for privacy.
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

# POST one event per skill (fire-and-forget).
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

// WriteCursorStopScript creates ~/.skael/hooks/skael-cursor-stop.sh with 0755 permissions.
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
```

- [ ] **Step 2: Update WriteHookScript to also write the cursor script**

In `cli/hooks/script.go`, update `WriteHookScript` to also call `WriteCursorStopScript`:

Actually, keep them separate. `WriteHookScript` writes the PreToolUse script; `WriteCursorStopScript` writes the stop script. The setup flow will call both. No change to `script.go` needed.

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git add cli/hooks/cursor_script.go
git commit -m "feat(cursor): add stop-hook transcript parsing script"
```

---

### Task 3: Hook installation for Cursor

**Files:**
- Modify: `cli/hooks/install.go`

- [ ] **Step 1: Add installCursorHook and uninstallCursorHook**

At the end of `install.go`, before the "Generic dispatch" section, add:

```go
// ────────────────────────────────────────────────────────────────────────────
// Cursor  (JSON hooks.json)
// ────────────────────────────────────────────────────────────────────────────

// installCursorHook writes a stop hook entry to configPath (~/.cursor/hooks.json).
func installCursorHook(configPath, scriptPath string) error {
	hooks, err := readJSONFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read cursor hooks: %w", err)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}

	// Ensure version key.
	if _, ok := hooks["version"]; !ok {
		hooks["version"] = float64(1)
	}

	// Ensure hooks object.
	hooksObj, ok := hooks["hooks"].(map[string]any)
	if !ok {
		hooksObj = map[string]any{}
		hooks["hooks"] = hooksObj
	}

	// Ensure stop array.
	stopArr, ok := hooksObj["stop"].([]any)
	if !ok {
		stopArr = []any{}
	}

	cmd := fmt.Sprintf("SKAEL_AGENT=cursor %s", scriptPath)

	newEntry := map[string]any{
		"_managed_by": managedBy,
		"command":     cmd,
	}

	// Check for existing skael entry.
	found := false
	for i, entry := range stopArr {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if m["_managed_by"] == managedBy {
			stopArr[i] = newEntry
			found = true
			break
		}
	}
	if !found {
		stopArr = append(stopArr, newEntry)
	}

	hooksObj["stop"] = stopArr
	return writeJSONFile(configPath, hooks)
}

// uninstallCursorHook removes the skael-managed stop hook from configPath.
func uninstallCursorHook(configPath string) error {
	hooks, err := readJSONFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read cursor hooks: %w", err)
	}

	hooksObj, ok := hooks["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	stopArr, ok := hooksObj["stop"].([]any)
	if !ok {
		return nil
	}

	var filtered []any
	for _, entry := range stopArr {
		m, ok := entry.(map[string]any)
		if ok && m["_managed_by"] == managedBy {
			continue
		}
		filtered = append(filtered, entry)
	}

	if len(filtered) == 0 {
		delete(hooksObj, "stop")
	} else {
		hooksObj["stop"] = filtered
	}

	// Clean up empty hooks object.
	if len(hooksObj) == 0 {
		delete(hooks, "hooks")
	}

	return writeJSONFile(configPath, hooks)
}
```

- [ ] **Step 2: Update InstallForAgent and UninstallForAgent routing**

In the `InstallForAgent` switch, add:

```go
case "cursor":
    return installCursorHook(configPath, scriptPath)
```

Note: `installCursorHook` only takes `configPath` and `scriptPath` (not `endpoint` or `apiKey` since the script reads from config.json at runtime). But the function signature has all four params. Just ignore the unused ones.

In the `UninstallForAgent` switch, add:

```go
case "cursor":
    return uninstallCursorHook(configPath)
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git add cli/hooks/install.go
git commit -m "feat(cursor): add hook installation for Cursor stop hook"
```

---

### Task 4: Update setup to write cursor script

**Files:**
- Modify: `cli/setup.go`

- [ ] **Step 1: Write cursor stop script during setup**

In `cli/setup.go`, find line 98 (`scriptPath, err := hooks.WriteHookScript(dir)`). After line 101 (the closing `} else {`'s error case), add before the `for` loop:

```go
cursorScriptPath, cursorErr := hooks.WriteCursorStopScript(dir)
if cursorErr != nil {
    ui.Warn("write cursor hook script: %s", cursorErr)
}
```

Then change the loop body (lines 102-113) to select the right script per agent:

```go
for _, agent := range detectedAgents {
    configPath := agent.ConfigPath(home)
    if mkErr := os.MkdirAll(filepath.Dir(configPath), 0o755); mkErr != nil {
        ui.Warn("create config dir for %s: %s", agent.Name(), mkErr)
        continue
    }
    hookScript := scriptPath
    if agent.Name() == "cursor" {
        hookScript = cursorScriptPath
    }
    if instErr := hooks.InstallForAgent(agent.Name(), configPath, endpoint, apiKey, hookScript); instErr != nil {
        ui.Warn("install hook for %s: %s", agent.Name(), instErr)
    } else {
        ui.Success("Hook installed for %s", agent.Name())
    }
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`

- [ ] **Step 3: Commit**

```bash
git add cli/setup.go
git commit -m "feat(cursor): write cursor stop script during setup"
```

---

### Task 5: Tests

**Files:**
- Create: `cli/agents/cursor_test.go`

- [ ] **Step 1: Write detection test**

```go
// cli/agents/cursor_test.go
package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCursor_Detected(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)

	detected := DetectIn(home)
	assert.Len(t, detected, 1)
	assert.Equal(t, "cursor", detected[0].Name())
	assert.Equal(t, filepath.Join(home, ".cursor", "skills"), detected[0].SkillsDir(home))
	assert.Equal(t, filepath.Join(home, ".cursor", "hooks.json"), detected[0].ConfigPath(home))
}

func TestCursor_NotDetected(t *testing.T) {
	home := t.TempDir()
	c := &Cursor{}
	assert.False(t, c.Detected(home))
}
```

- [ ] **Step 2: Run tests**

Run: `cd /Users/nathananderson-tennant/Development/skael && go test ./cli/agents/ -run TestCursor -v -count=1`
Expected: Both tests pass.

- [ ] **Step 3: Run all hook tests to verify nothing broke**

Run: `go test ./cli/hooks/ -v -count=1`
Expected: All existing tests still pass.

- [ ] **Step 4: Run full build**

Run: `go build ./... && go vet ./...`

- [ ] **Step 5: Commit**

```bash
git add cli/agents/cursor_test.go
git commit -m "feat(cursor): add detection tests"
```

---

### Task 6: End-to-end verification

- [ ] **Step 1: Build CLI**

```bash
cd /Users/nathananderson-tennant/Development/skael && go build -o bin/skael ./cmd/skael/
```

- [ ] **Step 2: Verify Cursor is detected**

If `~/.cursor/` exists:
```bash
bin/skael doctor
```
Expected: Shows "cursor: hook installed" or "cursor: hook not installed" alongside the other agents.

If not, create it temporarily:
```bash
mkdir -p ~/.cursor && bin/skael doctor && rmdir ~/.cursor
```

- [ ] **Step 3: Test hook install**

```bash
mkdir -p ~/.cursor
bin/skael hook install
cat ~/.cursor/hooks.json
```

Expected: JSON with `"stop"` array containing the skael entry.

- [ ] **Step 4: Test hook uninstall**

```bash
bin/skael hook uninstall
cat ~/.cursor/hooks.json
```

Expected: The skael entry is removed.

- [ ] **Step 5: Verify the stop script was written**

```bash
cat ~/.skael/hooks/skael-cursor-stop.sh | head -5
```

Expected: Shows the shebang and "managed by skael CLI" comment.

- [ ] **Step 6: Commit any fixups**

```bash
git add -A && git commit -m "fix(cursor): e2e verification fixups"
```
