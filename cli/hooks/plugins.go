package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// reporterTS posts one activation to the server, fire and forget. Plugins
// read the endpoint and key from ~/.skael/config.json at runtime, so no
// credential is written into a plugin file.
const reporterTS = `import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

const SKILL_NAME = /^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$/

function readJSON(path: string): any {
  try {
    return JSON.parse(readFileSync(path, "utf8"))
  } catch {
    return null
  }
}

function hash(value: string): string {
  return createHash("sha256").update(value).digest("hex").slice(0, 16)
}

function report(agent: string, skill: string, extra: { model?: string; harness_version?: string } = {}) {
  if (skill.length > 128 || !SKILL_NAME.test(skill)) return
  const config = readJSON(join(homedir(), ".skael", "config.json"))
  if (!config?.endpoint || !config?.api_key) return
  const state = readJSON(join(homedir(), ".skael", "state.json"))
  const version = state?.skills?.find((s: { name: string }) => s.name === skill)?.version ?? 0
  fetch(String(config.endpoint).replace(/\/+$/, "") + "/api/events", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + config.api_key },
    body: JSON.stringify({
      skill_name: skill,
      version,
      agent,
      event_source: "tool_invocation",
      trigger_type: "auto",
      project_hash: hash(process.cwd()),
      developer_hash: hash((process.env.USER ?? "unknown") + "@" + (process.env.HOSTNAME ?? "unknown")),
      ...extra,
    }),
  }).catch(() => {})
}
`

// opencodePlugin reports each call of OpenCode's skill tool, whose argument
// is the skill's name.
const opencodePlugin = `// skael-tracking.ts — managed by skael CLI
` + reporterTS + `
export default async () => ({
  "tool.execute.before": async (input: { tool: string }, output: { args: any }) => {
    if (input.tool !== "skill") return
    const name = output?.args?.name
    if (typeof name === "string") report("opencode", name)
  },
})
`

// piExtension reports a skill pi loads: the model reads a SKILL.md inside a
// skills folder, or the user types /skill:name. pi has no skill tool.
const piExtension = `// skael-tracking.ts — managed by skael CLI
import { basename, dirname } from "node:path"
` + reporterTS + `
let harness = ""
import("@earendil-works/pi-coding-agent").then((m: any) => { harness = String(m.VERSION ?? "") }).catch(() => {})

function extra(ctx: any) {
  const model = typeof ctx?.model?.id === "string" ? ctx.model.id : undefined
  return { model, harness_version: harness || undefined }
}

export default function (pi: any) {
  pi.on("tool_call", async (event: any, ctx: any) => {
    if (event.toolName !== "read") return
    const path = String(event.input?.path ?? "")
    if (basename(path) !== "SKILL.md" || basename(dirname(dirname(path))) !== "skills") return
    report("pi", basename(dirname(path)), extra(ctx))
  })
  pi.on("input", async (event: any, ctx: any) => {
    const m = /^\/skill:(\S+)/.exec(event.text ?? "")
    if (m) report("pi", m[1], extra(ctx))
    return { action: "continue" }
  })
}
`

// autoSyncTS runs the debounced auto-sync script when a session starts. The
// script returns at once and syncs in the background.
func autoSyncTS(agent, scriptPath string) string {
	script, _ := json.Marshal(scriptPath)
	run := `function autoSync() {
  try {
    spawn("bash", [` + string(script) + `], { detached: true, stdio: "ignore" }).unref()
  } catch {}
}
`
	head := "// skael-autosync.ts — managed by skael CLI\nimport { spawn } from \"node:child_process\"\n\n" + run
	if agent == "pi" {
		return head + `
export default function (pi: any) {
  pi.on("session_start", async () => autoSync())
}
`
	}
	return head + `
export default async () => ({
  event: async ({ event }: { event: { type: string } }) => {
    if (event.type === "session.created") autoSync()
  },
})
`
}

// autoSyncPluginPath is the auto-sync plugin beside an agent's tracking
// plugin, so each can be installed and removed on its own.
func autoSyncPluginPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "skael-autosync.ts")
}

func writePlugin(path, source string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(path, []byte(source), 0o644)
}

// removePlugin removes a plugin file skael wrote, and nothing else.
func removePlugin(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "managed by skael CLI") {
		return nil
	}
	return os.Remove(path)
}
