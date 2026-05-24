# OpenCode Agent Integration

Add OpenCode as a supported agent in Skael — agent detection, skill sync, and activation tracking via a TypeScript plugin hook.

## Context

Skael currently supports Claude Code and Codex CLI. Both use a shared bash hook script (`~/.skael/hooks/skael-hook.sh`) triggered from agent-specific config entries (JSON for Claude, TOML for Codex).

OpenCode is architecturally different: its hook system uses TypeScript plugins loaded by Bun, not shell commands in a config file. The activation tracking hook is a `.ts` file placed in the plugins directory, replacing both the bash script and the config entry.

## Scope

- Agent detection for OpenCode
- Skill sync to OpenCode's native skills directory
- TypeScript plugin for activation tracking (embedded in Go binary, written at install time)
- Install/uninstall/status CLI support
- Tests matching existing coverage patterns

Out of scope: npm package distribution (post-MVP), OpenTelemetry integration, subagent tracking.

## Agent Detection

New file: `cli/agents/opencode.go`

```go
type OpenCode struct{}

func (o *OpenCode) Name() string          { return "opencode" }
func (o *OpenCode) SkillsDir(home string) string  { return filepath.Join(home, ".config", "opencode", "skills") }
func (o *OpenCode) ConfigPath(home string) string  { return filepath.Join(home, ".config", "opencode", "plugins", "skael-tracking.ts") }
func (o *OpenCode) Detected(home string) bool      { return dirExists(filepath.Join(home, ".config", "opencode")) }
```

Changes to existing files:
- `cli/agents/agent.go`: add `&OpenCode{}` to the `known` slice in `DetectIn()`
- `cli/hook.go`: add `&agents.OpenCode{}` to `knownAgents` in `runHookStatus()`

### Detection method

Check if `~/.config/opencode/` directory exists. OpenCode uses `~/.config/opencode/` on both macOS and Linux (XDG convention). No binary-in-PATH check needed — the directory is created on first run.

### Skills directory

`~/.config/opencode/skills/` — OpenCode's native skills path. OpenCode also reads `.claude/skills/` as a fallback, but we place skills in the native directory for proper ownership.

## Skill Sync

No new code required. The existing sync command iterates all detected agents and extracts skills to `agent.SkillsDir(home)`. Adding OpenCode to the known agents list is sufficient.

## TypeScript Plugin Hook

New file: `cli/hooks/opencode_plugin.go`

Contains a Go string constant with the TypeScript plugin source and a `WriteOpenCodePlugin()` function.

### Plugin source (embedded as Go constant)

```typescript
// skael-tracking.ts — managed by skael CLI
// Activation tracking plugin for OpenCode.
// Reads credentials from ~/.skael/config.json at runtime.
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
        .update(`${process.env.USER ?? "unknown"}@${process.env.HOSTNAME ?? "unknown"}`)
        .digest("hex")
        .slice(0, 16)

      fetch(`${config.endpoint}/api/events`, {
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
```

### Design decisions

- **`readFileSync` for config**: Config is read synchronously on each hook invocation. The file is tiny (~100 bytes) and local — async adds complexity for no benefit. If the file is missing or malformed, the hook silently exits.
- **`os.homedir()` not `~`**: Bun doesn't expand `~` in file paths. Using Node's `os.homedir()` works in Bun.
- **Fire-and-forget**: `fetch().catch(() => {})` — the promise is not awaited, so hook latency is near-zero. Even if `tool.execute.before` awaits the returned promise, the fetch runs in the background.
- **No `@opencode-ai/plugin` runtime dependency**: The import is type-only (`type Plugin`). At runtime it's a plain function export. OpenCode's plugin loader provides the type resolution.
- **`input.tool` as skill name**: OpenCode passes the tool name in `input.tool`. This captures which tool was invoked (e.g., "bash", "edit", "read", or a custom skill-provided tool).
- **Credentials never in plugin file**: Same security model as the bash script — reads from `~/.skael/config.json` at runtime.

### No separate write function

Unlike the bash hook (which has `WriteHookScript` because multiple agents share it), the OpenCode plugin is self-contained. `installOpenCodeHook` writes the plugin constant directly to `configPath` (`~/.config/opencode/plugins/skael-tracking.ts`). No intermediate staging or shared script needed.

## Install / Uninstall

Changes to `cli/hooks/install.go`:

### `installOpenCodeHook`

```go
func installOpenCodeHook(configPath string) error {
    if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
        return err
    }
    return atomicWriteFile(configPath, []byte(opencodePlugin), 0o644)
}
```

Simpler than Claude/Codex because the hook is a standalone file, not an entry in a shared config. Idempotent — overwrites with current version.

The `endpoint`, `apiKey`, and `scriptPath` parameters are unused (credentials come from `~/.skael/config.json` at runtime, and the plugin is self-contained). The function signature in `InstallForAgent` passes them but `installOpenCodeHook` ignores them.

### `uninstallOpenCodeHook`

```go
func uninstallOpenCodeHook(configPath string) error {
    err := os.Remove(configPath)
    if os.IsNotExist(err) {
        return nil
    }
    return err
}
```

### Dispatch updates

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

## Tests

New tests in `cli/hooks/hooks_test.go`:

| Test | Validates |
|------|-----------|
| `TestInstallOpenCodeHook_NewFile` | Plugin file created with correct content |
| `TestInstallOpenCodeHook_Idempotent` | Two installs produce one file with same content |
| `TestUninstallOpenCodeHook` | File removed cleanly |
| `TestUninstallOpenCodeHook_NotExists` | No error when file doesn't exist |
| `TestOpenCodePlugin_NoPlaintextAPIKey` | No credentials embedded in plugin source |
| `TestOpenCodePlugin_FireAndForget` | Contains `.catch(() => {})` pattern |
| `TestOpenCodePlugin_ReadsConfigFile` | References `config.json` for credentials |

New test file `cli/agents/agents_test.go`:

| Test | Validates |
|------|-----------|
| `TestOpenCode_Detected` | Returns true when `~/.config/opencode/` exists |
| `TestOpenCode_NotDetected` | Returns false when directory absent |
| `TestDetectIn_IncludesOpenCode` | OpenCode appears in `DetectIn()` results |
| `TestOpenCode_SkillsDir` | Returns correct path |
| `TestOpenCode_ConfigPath` | Returns correct plugin path |

## Known Limitations

1. **Subagent blind spot**: `tool.execute.before` does not fire for tools invoked by subagents spawned via OpenCode's `task` tool (upstream issue #5894). Some activations will be missed.
2. **Plugin API coupling**: If OpenCode ships a breaking change to the plugin API, the embedded plugin breaks until the user updates their Skael CLI. Same risk profile as the bash script if Claude changes hook stdin format.
3. **Bun runtime required**: The plugin runs under Bun, which ships with OpenCode. Not an additional dependency.

## Post-MVP: npm Package

Future enhancement: publish `@skael/opencode-plugin` to npm. Users would add it to `opencode.json`:

```json
{
  "plugin": ["@skael/opencode-plugin"]
}
```

Benefits: versioned independently from CLI, auto-installed by OpenCode, follows OpenCode's preferred distribution model. Could grow to include custom tools (skill search), UI integration, and richer telemetry.

The embedded file approach is the right v1 — CLI-managed lifecycle, no external dependencies, proven pattern from bash hook. The npm package is the natural v2 when we want tighter OpenCode ecosystem integration.
