# OpenCode Agent Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add OpenCode as a third supported agent in Skael — detection, skill sync, and activation tracking via a TypeScript plugin hook.

**Architecture:** OpenCode uses TypeScript plugins (loaded by Bun) instead of shell commands in config files. The hook is a `.ts` file written directly to `~/.config/opencode/plugins/skael-tracking.ts` — no shared bash script, no config-file manipulation. The Go binary embeds the TypeScript source as a string constant and writes it at install time.

**Tech Stack:** Go (CLI), TypeScript (embedded plugin source), testify (assertions)

**Spec:** `docs/superpowers/specs/2026-05-24-opencode-hooks-design.md`

---

### File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `cli/agents/opencode.go` | OpenCode agent struct implementing `Agent` interface |
| Modify | `cli/agents/agent.go:26-28` | Add `&OpenCode{}` to `known` slice |
| Modify | `cli/agents/agents_test.go` | Add OpenCode detection tests |
| Create | `cli/hooks/opencode_plugin.go` | Embedded TypeScript plugin source constant |
| Modify | `cli/hooks/install.go:242-263` | Add `installOpenCodeHook`/`uninstallOpenCodeHook` + dispatch cases |
| Modify | `cli/hooks/hooks_test.go` | Add OpenCode install/uninstall/content tests |
| Modify | `cli/hook.go:98-100` | Add `&agents.OpenCode{}` to `knownAgents` in `runHookStatus` |

---

### Task 1: OpenCode Agent Detection

**Files:**
- Create: `cli/agents/opencode.go`
- Modify: `cli/agents/agent.go:26-28`
- Test: `cli/agents/agents_test.go`

- [ ] **Step 1: Write failing tests for OpenCode detection**

Add three tests to `cli/agents/agents_test.go`:

```go
func TestDetect_OpenCode(t *testing.T) {
	home := t.TempDir()
	opencodeDir := filepath.Join(home, ".config", "opencode")
	require.NoError(t, os.MkdirAll(opencodeDir, 0o755))

	detected := DetectIn(home)

	require.Len(t, detected, 1)
	assert.Equal(t, "opencode", detected[0].Name())
	assert.Equal(t, filepath.Join(home, ".config", "opencode", "skills"), detected[0].SkillsDir(home))
	assert.Equal(t, filepath.Join(home, ".config", "opencode", "plugins", "skael-tracking.ts"), detected[0].ConfigPath(home))
}

func TestDetect_MultipleAgents(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755))

	detected := DetectIn(home)

	require.Len(t, detected, 2)
	names := []string{detected[0].Name(), detected[1].Name()}
	assert.Contains(t, names, "claude-code")
	assert.Contains(t, names, "opencode")
}

func TestOpenCode_NotDetected(t *testing.T) {
	home := t.TempDir()

	oc := &OpenCode{}
	assert.False(t, oc.Detected(home))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && go test ./cli/agents/ -run "TestDetect_OpenCode|TestDetect_MultipleAgents|TestOpenCode_NotDetected" -v`

Expected: compilation error — `OpenCode` type not defined.

- [ ] **Step 3: Create `cli/agents/opencode.go`**

```go
package agents

import "path/filepath"

// OpenCode represents the OpenCode AI coding agent.
type OpenCode struct{}

func (o *OpenCode) Name() string { return "opencode" }

func (o *OpenCode) SkillsDir(home string) string {
	return filepath.Join(home, ".config", "opencode", "skills")
}

func (o *OpenCode) ConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "plugins", "skael-tracking.ts")
}

func (o *OpenCode) Detected(home string) bool {
	return dirExists(filepath.Join(home, ".config", "opencode"))
}
```

- [ ] **Step 4: Add OpenCode to the known agents list in `agent.go`**

In `cli/agents/agent.go`, change the `known` slice in `DetectIn()` (line 26-28) from:

```go
	known := []Agent{
		&ClaudeCode{},
		&Codex{},
	}
```

to:

```go
	known := []Agent{
		&ClaudeCode{},
		&Codex{},
		&OpenCode{},
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && go test ./cli/agents/ -v`

Expected: all tests pass, including existing `TestDetect_ClaudeCode`, `TestDetect_Codex`, `TestDetect_NoneInstalled`, and the three new ones.

- [ ] **Step 6: Commit**

```bash
cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks
git add cli/agents/opencode.go cli/agents/agent.go cli/agents/agents_test.go
git commit -m "feat: add OpenCode agent detection"
```

---

### Task 2: TypeScript Plugin Constant

**Files:**
- Create: `cli/hooks/opencode_plugin.go`

- [ ] **Step 1: Create `cli/hooks/opencode_plugin.go` with the embedded plugin source**

```go
package hooks

// opencodePlugin is the TypeScript source for the OpenCode activation tracking plugin.
// It hooks into tool.execute.before and POSTs activation events to the Skael server.
// Credentials are read from ~/.skael/config.json at runtime — never embedded in the plugin file.
const opencodePlugin = `// skael-tracking.ts — managed by skael CLI
import { type Plugin } from "@opencode-ai/plugin"
import { readFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"

interface SkaalConfig {
  endpoint?: string
  api_key?: string
}

function loadConfig(): SkaalConfig | null {
  try {
    const raw = readFileSync(join(homedir(), ".skael", "config.json"), "utf-8")
    return JSON.parse(raw) as SkaalConfig
  } catch {
    return null
  }
}

export default (async () => {
  return {
    "tool.execute.before": async (input: { tool: string }) => {
      const config = loadConfig()
      if (!config?.endpoint || !config?.api_key) return

      const { createHash } = await import("crypto")
      const projectHash = createHash("sha256").update(process.cwd()).digest("hex").slice(0, 16)
      const devHash = createHash("sha256")
        .update(` + "`${process.env.USER ?? \"unknown\"}@${process.env.HOSTNAME ?? \"unknown\"}`" + `)
        .digest("hex")
        .slice(0, 16)

      fetch(` + "`${config.endpoint}/api/events`" + `, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-API-Key": config.api_key,
        },
        body: JSON.stringify({
          skill_name: input.tool,
          agent: "opencode",
          trigger_type: "auto",
          project_hash: projectHash,
          developer_hash: devHash,
        }),
      }).catch(() => {})
    },
  }
}) satisfies Plugin
`
```

Note: The backtick template literals in the TypeScript need to be broken out of the Go raw string literal. The pattern above uses string concatenation to embed them.

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && go build ./cli/hooks/`

Expected: compiles with no errors.

- [ ] **Step 3: Commit**

```bash
cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks
git add cli/hooks/opencode_plugin.go
git commit -m "feat: add embedded OpenCode TypeScript plugin source"
```

---

### Task 3: Install and Uninstall Functions

**Files:**
- Modify: `cli/hooks/install.go:242-263`
- Test: `cli/hooks/hooks_test.go`

- [ ] **Step 1: Write failing tests for OpenCode hook install/uninstall**

Add these tests to `cli/hooks/hooks_test.go`:

```go
func TestInstallOpenCodeHook_NewFile(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	configPath := filepath.Join(pluginsDir, "skael-tracking.ts")

	err := hooks.InstallForAgent("opencode", configPath, "https://skael.example.com", "test-api-key", "/unused")
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "managed by skael", "plugin must contain managed-by marker")
	assert.Contains(t, content, "tool.execute.before", "plugin must hook tool.execute.before")
	assert.Contains(t, content, "/api/events", "plugin must POST to /api/events")
	assert.Contains(t, content, `agent: "opencode"`, "plugin must identify as opencode agent")
}

func TestInstallOpenCodeHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	configPath := filepath.Join(pluginsDir, "skael-tracking.ts")

	require.NoError(t, hooks.InstallForAgent("opencode", configPath, "https://skael.example.com", "key1", "/unused"))
	require.NoError(t, hooks.InstallForAgent("opencode", configPath, "https://skael.example.com", "key2", "/unused"))

	data1, err := os.ReadFile(configPath)
	require.NoError(t, err)

	// Content should be identical after two installs (same constant written both times).
	require.NoError(t, hooks.InstallForAgent("opencode", configPath, "https://skael.example.com", "key3", "/unused"))

	data2, err := os.ReadFile(configPath)
	require.NoError(t, err)

	assert.Equal(t, string(data1), string(data2), "plugin content must be identical after repeated installs")
}

func TestUninstallOpenCodeHook(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	configPath := filepath.Join(pluginsDir, "skael-tracking.ts")

	require.NoError(t, hooks.InstallForAgent("opencode", configPath, "https://skael.example.com", "test-key", "/unused"))

	// Confirm file exists after install.
	_, err := os.Stat(configPath)
	require.NoError(t, err, "plugin file must exist after install")

	require.NoError(t, hooks.UninstallForAgent("opencode", configPath))

	// File must be gone.
	_, err = os.Stat(configPath)
	assert.True(t, os.IsNotExist(err), "plugin file must be removed after uninstall")
}

func TestUninstallOpenCodeHook_NotExists(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "nonexistent", "skael-tracking.ts")

	err := hooks.UninstallForAgent("opencode", configPath)
	assert.NoError(t, err, "uninstalling a nonexistent plugin must not error")
}

func TestOpenCodePlugin_NoPlaintextCredentials(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	configPath := filepath.Join(pluginsDir, "skael-tracking.ts")

	const sensitiveKey = "super-secret-api-key-12345"
	const sensitiveEndpoint = "https://secret.skael.example.com"

	err := hooks.InstallForAgent("opencode", configPath, sensitiveEndpoint, sensitiveKey, "/unused")
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	assert.NotContains(t, string(data), sensitiveKey, "API key must NOT appear in plugin source")
	assert.NotContains(t, string(data), sensitiveEndpoint, "endpoint must NOT appear in plugin source")
}

func TestOpenCodePlugin_FireAndForget(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	configPath := filepath.Join(pluginsDir, "skael-tracking.ts")

	require.NoError(t, hooks.InstallForAgent("opencode", configPath, "https://skael.example.com", "key", "/unused"))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	assert.Contains(t, string(data), ".catch(() => {})", "plugin must use fire-and-forget fetch pattern")
}

func TestOpenCodePlugin_ReadsConfigFile(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	configPath := filepath.Join(pluginsDir, "skael-tracking.ts")

	require.NoError(t, hooks.InstallForAgent("opencode", configPath, "https://skael.example.com", "key", "/unused"))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	assert.Contains(t, string(data), "config.json", "plugin must read credentials from config.json")
	assert.Contains(t, string(data), "homedir()", "plugin must use os.homedir() for path resolution")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && go test ./cli/hooks/ -run "TestInstallOpenCodeHook|TestUninstallOpenCodeHook|TestOpenCodePlugin" -v`

Expected: FAIL — `unsupported agent: opencode` from the `InstallForAgent` default case.

- [ ] **Step 3: Add install/uninstall functions and dispatch cases to `install.go`**

Add the OpenCode section before the "Generic dispatch" section (before line 237). Insert after the Codex `replaceCodexBlock` function (after line 235):

```go
// ────────────────────────────────────────────────────────────────────────────
// OpenCode  (TypeScript plugin file)
// ────────────────────────────────────────────────────────────────────────────

// installOpenCodeHook writes the skael TypeScript plugin to configPath.
// Unlike Claude/Codex, this is a standalone file — not an entry in a shared config.
func installOpenCodeHook(configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(configPath, []byte(opencodePlugin), 0o644)
}

// uninstallOpenCodeHook removes the skael TypeScript plugin file.
func uninstallOpenCodeHook(configPath string) error {
	err := os.Remove(configPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
```

Add `"path/filepath"` to the imports at the top of `install.go`.

Update `InstallForAgent` — add the `"opencode"` case:

```go
func InstallForAgent(agentName, configPath, endpoint, apiKey, scriptPath string) error {
	switch agentName {
	case "claude-code":
		return InstallClaudeHook(configPath, endpoint, apiKey, scriptPath)
	case "codex":
		return installCodexHook(configPath, endpoint, apiKey, scriptPath)
	case "opencode":
		return installOpenCodeHook(configPath)
	default:
		return fmt.Errorf("unsupported agent: %s", agentName)
	}
}
```

Update `UninstallForAgent` — add the `"opencode"` case:

```go
func UninstallForAgent(agentName, configPath string) error {
	switch agentName {
	case "claude-code":
		return UninstallClaudeHook(configPath)
	case "codex":
		return uninstallCodexHook(configPath)
	case "opencode":
		return uninstallOpenCodeHook(configPath)
	default:
		return fmt.Errorf("unsupported agent: %s", agentName)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && go test ./cli/hooks/ -v`

Expected: all tests pass — existing Claude/Codex tests plus all new OpenCode tests.

- [ ] **Step 5: Commit**

```bash
cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks
git add cli/hooks/install.go cli/hooks/hooks_test.go
git commit -m "feat: add OpenCode hook install/uninstall with tests"
```

---

### Task 4: Update CLI Hook Status

**Files:**
- Modify: `cli/hook.go:98-100`

- [ ] **Step 1: Add OpenCode to the known agents list in `runHookStatus`**

In `cli/hook.go`, change the `knownAgents` slice in `runHookStatus()` (line 98-100) from:

```go
	knownAgents := []agents.Agent{
		&agents.ClaudeCode{},
		&agents.Codex{},
	}
```

to:

```go
	knownAgents := []agents.Agent{
		&agents.ClaudeCode{},
		&agents.Codex{},
		&agents.OpenCode{},
	}
```

- [ ] **Step 2: Verify the full CLI builds**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && go build ./cmd/skael/`

Expected: compiles with no errors.

- [ ] **Step 3: Commit**

```bash
cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks
git add cli/hook.go
git commit -m "feat: include OpenCode in hook status output"
```

---

### Task 5: Full Test Suite Verification

- [ ] **Step 1: Run all fast tests**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && just test-fast`

Expected: all tests pass, no regressions.

- [ ] **Step 2: Build both binaries**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && just build`

Expected: both `bin/server` and `bin/skael` build successfully.

- [ ] **Step 3: Smoke-test the CLI help output**

Run: `cd /Users/nathananderson-tennant/Development/skael/.claude/worktrees/opencode-hooks && ./bin/skael hook status 2>&1 || true`

Expected: output includes a line for `opencode` (either "not detected" or "hook not installed" depending on whether `~/.config/opencode/` exists on this machine).
