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
