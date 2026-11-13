package hooks

import (
	"encoding/json"
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

// piExtension reports a skill pi loads: the model reads a SKILL.md directly
// inside a skills folder pi loads from, or the user types /skill:name. pi has
// no skill tool. A SKILL.md anywhere else, such as a skill repo's working
// copy, is a file read.
const piExtension = `// skael-tracking.ts — managed by skael CLI
import { basename, dirname, resolve } from "node:path"
` + reporterTS + `
let harness = ""
import("@earendil-works/pi-coding-agent").then((m: any) => { harness = String(m.VERSION ?? "") }).catch(() => {})

const SKILL_ROOTS = ["/.agents/skills", "/.pi/skills", "/.pi/agent/skills"]

function extra(ctx: any) {
  const model = typeof ctx?.model?.id === "string" ? ctx.model.id : undefined
  return { model, harness_version: harness || undefined }
}

// A skill skael installed sits in a folder named exactly its canonical name.
// Any other skill is named by its frontmatter, then by its folder.
function skillName(file: string): string {
  const folder = basename(dirname(file))
  const state = readJSON(join(homedir(), ".skael", "state.json"))
  if (state?.skills?.some((s: { name: string }) => s.name === folder)) return folder
  try {
    const lines = readFileSync(file, "utf8").split(/\r?\n/)
    if (lines[0] === "---") {
      for (const line of lines.slice(1)) {
        if (line === "---") break
        const m = /^name:\s*["']?([^"'\s]+)["']?\s*$/.exec(line)
        if (m) return m[1]
      }
    }
  } catch {}
  return folder
}

export default function (pi: any) {
  pi.on("tool_call", async (event: any, ctx: any) => {
    if (event.toolName !== "read") return
    let path = String(event.input?.path ?? "")
    if (path.startsWith("~/")) path = join(homedir(), path.slice(2))
    path = resolve(typeof ctx?.cwd === "string" ? ctx.cwd : process.cwd(), path)
    if (basename(path) !== "SKILL.md") return
    const root = dirname(dirname(path)).replace(/\\/g, "/")
    if (!SKILL_ROOTS.some((r) => root.endsWith(r))) return
    report("pi", skillName(path), extra(ctx))
  })
  pi.on("input", async (event: any, ctx: any) => {
    const m = /^\/skill:(\S+)/.exec(event.text ?? "")
    if (m) report("pi", m[1], extra(ctx))
    return { action: "continue" }
  })
}
`

// autoSyncTS runs `skael hook autosync` when a session starts. pi and Bun
// spawn without a shell, so the binary is named directly.
func autoSyncTS(agent, bin string) string {
	argv, _ := json.Marshal(autoSyncArgs)
	path, _ := json.Marshal(bin)
	run := `function autoSync() {
  try {
    spawn(` + string(path) + `, ` + string(argv) + `, { detached: true, stdio: "ignore", windowsHide: true }).unref()
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

const pluginMarker = "managed by skael CLI"

// pluginTarget is a plugin file skael owns whole. Removing it deletes only a
// file that carries the marker.
func pluginTarget(path, source string) target {
	return target{
		path:    path,
		install: func([]byte) ([]byte, error) { return []byte(source), nil },
		remove: func(data []byte) ([]byte, error) {
			if strings.Contains(string(data), pluginMarker) {
				return nil, nil
			}
			return data, nil
		},
		has:       func(data []byte) bool { return strings.Contains(string(data), pluginMarker) },
		normalize: identity,
	}
}
