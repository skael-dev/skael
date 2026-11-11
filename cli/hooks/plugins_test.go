package hooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runPlugin loads a plugin in node with a home whose config points at a fake
// server, runs driver against its default export, and returns the events the
// server received.
func runPlugin(t *testing.T, source, driver string) []map[string]any {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	events := make(chan map[string]any, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev map[string]any
		_ = json.Unmarshal(body, &ev)
		events <- ev
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	home, dir := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".skael"), 0o755))
	cfg, _ := json.Marshal(map[string]string{"endpoint": srv.URL + "/", "api_key": "k"})
	require.NoError(t, os.WriteFile(filepath.Join(home, ".skael", "config.json"), cfg, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".skael", "state.json"), []byte(`{"skills":[{"name":"deploy-helper","version":3}]}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.ts"), []byte(source), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "driver.mts"), []byte(`import plugin from "./plugin.ts"
`+driver+`
await new Promise((r) => setTimeout(r, 500))
`), 0o644))

	cmd := exec.Command("node", "driver.mts")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	var got []map[string]any
	for {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-time.After(200 * time.Millisecond):
			return got
		}
	}
}

// OpenCode loads a skill through its skill tool, named by args.name. Any
// other tool is not an activation.
func TestOpenCodePluginReportsTheSkillTool(t *testing.T) {
	got := runPlugin(t, opencodePlugin, `const hooks = await plugin()
await hooks["tool.execute.before"]({ tool: "read" }, { args: { filePath: "x" } })
await hooks["tool.execute.before"]({ tool: "skill" }, { args: { name: "deploy-helper" } })
`)
	require.Len(t, got, 1)
	require.Equal(t, "deploy-helper", got[0]["skill_name"])
	require.Equal(t, "opencode", got[0]["agent"])
	require.EqualValues(t, 3, got[0]["version"])
}

// pi has no skill tool: a read of a SKILL.md in a skills folder, or
// /skill:name, is the activation.
func TestPiExtensionReportsASkillRead(t *testing.T) {
	got := runPlugin(t, piExtension, `const on = {}
plugin({ on: (name, fn) => { on[name] = fn } })
const ctx = { model: { id: "claude-sonnet-5-5" } }
await on.tool_call({ toolName: "read", input: { path: "/r/.agents/skills/deploy-helper/SKILL.md" } }, ctx)
await on.tool_call({ toolName: "read", input: { path: "/r/docs/SKILL.md" } }, ctx)
await on.tool_call({ toolName: "bash", input: { command: "cat SKILL.md" } }, ctx)
const res = await on.input({ text: "/skill:release-notes now" }, ctx)
if (res.action !== "continue") throw new Error("input must continue")
`)
	require.Len(t, got, 2)
	names := []any{got[0]["skill_name"], got[1]["skill_name"]}
	require.ElementsMatch(t, []any{"deploy-helper", "release-notes"}, names)
	for _, ev := range got {
		require.Equal(t, "pi", ev["agent"])
		require.Equal(t, "claude-sonnet-5-5", ev["model"])
	}
}
