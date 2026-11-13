package hooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/hooks"
)

const bin = "/opt/homebrew/bin/skael"

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// claudeHandlers returns every handler under event in a Claude settings.json.
func claudeHandlers(t *testing.T, path, event string) []map[string]any {
	t.Helper()
	var settings map[string]any
	require.NoError(t, json.Unmarshal([]byte(read(t, path)), &settings))
	hooksSection, _ := settings["hooks"].(map[string]any)
	list, _ := hooksSection[event].([]any)
	var out []map[string]any
	for _, raw := range list {
		group := raw.(map[string]any)
		inner, ok := group["hooks"].([]any)
		require.True(t, ok, "every %s entry must be a matcher group with a hooks array", event)
		for _, h := range inner {
			out = append(out, h.(map[string]any))
		}
	}
	return out
}

// Claude Code gets exec form: no shell parses the path, so Windows needs no
// Git Bash and a path with spaces needs no quoting.
func TestInstallClaude_ExecForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, hooks.InstallForAgent("claude-code", path, bin))
	require.NoError(t, hooks.InstallForAgent("claude-code", path, bin))

	for _, ev := range []string{"PreToolUse", "SessionStart", "PostModelSwitch"} {
		hs := claudeHandlers(t, path, ev)
		require.Len(t, hs, 1, ev)
		assert.Equal(t, bin, hs[0]["command"])
		assert.Equal(t, []any{"hook", "report", "--agent", "claude-code"}, hs[0]["args"])
		assert.Equal(t, "skael", hs[0]["_managed_by"])
	}
	assert.Contains(t, read(t, path), `"matcher": "Skill"`)

	require.NoError(t, hooks.UninstallForAgent("claude-code", path))
	assert.NotContains(t, read(t, path), "skael")
}

// Older releases wrote the auto-sync handler straight into UserPromptSubmit,
// outside a matcher group, where Claude Code never ran it.
func TestInstallClaudeAutoSync_ReplacesTheFlatEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"hooks":{"UserPromptSubmit":[{"type":"command","command":"/h/.skael/hooks/skael-autosync.sh","_managed_by":"skael-autosync"},{"hooks":[{"type":"command","command":"mine.sh"}]}]}}`
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0o644))

	require.NoError(t, hooks.InstallAutoSyncForAgent("claude-code", path, bin))

	hs := claudeHandlers(t, path, "UserPromptSubmit")
	require.Len(t, hs, 2)
	assert.Equal(t, "mine.sh", hs[0]["command"])
	assert.Equal(t, bin, hs[1]["command"])
	assert.Equal(t, []any{"hook", "autosync"}, hs[1]["args"])
	assert.NotContains(t, read(t, path), "skael-autosync.sh")

	require.NoError(t, hooks.UninstallAutoSyncForAgent("claude-code", path))
	assert.NotContains(t, read(t, path), "skael")
	assert.Contains(t, read(t, path), "mine.sh")
}

// The handler sits in a nested hooks table: Codex parses a flat command field
// as TOML and registers nothing. command_windows is what Codex runs in
// PowerShell, where the POSIX quoting of command fails.
func TestInstallCodex_Shape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	winBin := `C:\Users\Ada Lovelace\AppData\Local\Programs\skael\bin\skael.exe`
	require.NoError(t, hooks.InstallForAgent("codex", path, winBin))

	want := "# managed_by = skael\n[[hooks.PreToolUse]]\n\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\n" +
		`command = "'C:\\Users\\Ada Lovelace\\AppData\\Local\\Programs\\skael\\bin\\skael.exe' hook report --agent codex"` + "\n" +
		`command_windows = "& 'C:\\Users\\Ada Lovelace\\AppData\\Local\\Programs\\skael\\bin\\skael.exe' hook report --agent codex"` + "\n" +
		"# end managed_by = skael\n"
	assert.Equal(t, want, read(t, path))
}

// Reinstalling leaves the file byte for byte the same, so the repair on every
// command never rewrites a current config.
func TestInstallCodex_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`model = "gpt-5.5"`), 0o644))
	require.NoError(t, hooks.InstallForAgent("codex", path, bin))
	require.NoError(t, hooks.InstallAutoSyncForAgent("codex", path, bin))
	first := read(t, path)
	require.NoError(t, hooks.InstallForAgent("codex", path, bin))
	require.NoError(t, hooks.InstallAutoSyncForAgent("codex", path, bin))
	assert.Equal(t, first, read(t, path))
	assert.Equal(t, 2, strings.Count(first, "[[hooks.PreToolUse.hooks]]"))
	assert.True(t, hooks.State("codex", path, bin).ActivationCurrent)
	assert.True(t, hooks.State("codex", path, bin).AutoSyncCurrent)
}

// codexBlockStart is a substring of codexAutoSyncBlockStart, so a substring
// test would skip the activation install when only auto-sync is present.
func TestInstallCodex_AutoSyncFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, hooks.InstallAutoSyncForAgent("codex", path, bin))
	require.NoError(t, hooks.InstallForAgent("codex", path, bin))
	content := read(t, path)
	assert.Equal(t, 1, strings.Count(content, "# managed_by = skael\n"))
	assert.Equal(t, 1, strings.Count(content, "# managed_by = skael-autosync\n"))

	require.NoError(t, hooks.UninstallForAgent("codex", path))
	require.NoError(t, hooks.UninstallAutoSyncForAgent("codex", path))
	assert.NotContains(t, read(t, path), "PreToolUse")
}

func TestInstallCodex_UpgradesStaleBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	stale := "model = \"gpt-5.5\"\n\n# managed_by = skael\n[[hooks.pre_tool_use]]\ncommand = \"SKAEL_AGENT=codex /old/skael-hook.sh\"\n# end managed_by = skael\n"
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o644))
	assert.False(t, hooks.State("codex", path, bin).ActivationCurrent)

	require.NoError(t, hooks.Repair("codex", path, bin))
	content := read(t, path)
	assert.NotContains(t, content, "pre_tool_use")
	assert.NotContains(t, content, "skael-hook.sh")
	assert.Contains(t, content, `model = "gpt-5.5"`)
	assert.Equal(t, 1, strings.Count(content, "[[hooks.PreToolUse]]"))
	assert.NotContains(t, content, "autosync", "repair adds no hook the user did not install")
}

func cursorEntries(t *testing.T, path, event string) []map[string]any {
	t.Helper()
	var file map[string]any
	require.NoError(t, json.Unmarshal([]byte(read(t, path)), &file))
	assert.Equal(t, float64(1), file["version"])
	hooksObj, _ := file["hooks"].(map[string]any)
	list, _ := hooksObj[event].([]any)
	var out []map[string]any
	for _, raw := range list {
		out = append(out, raw.(map[string]any))
	}
	return out
}

// An install over the transcript-scanning stop hook removes it, so one skill
// load is not counted twice. The user's own hooks stay.
func TestInstallCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{"version":1,"hooks":{"stop":[{"command":"mine.sh"},{"_managed_by":"skael","command":"SKAEL_AGENT=cursor /h/.skael/hooks/skael-cursor-stop.sh"}]}}`
	require.NoError(t, os.WriteFile(path, []byte(existing), 0o644))

	require.NoError(t, hooks.InstallForAgent("cursor", path, bin))
	require.NoError(t, hooks.InstallForAgent("cursor", path, bin))
	require.NoError(t, hooks.InstallAutoSyncForAgent("cursor", path, bin))

	post := cursorEntries(t, path, "postToolUse")
	require.Len(t, post, 1)
	assert.Equal(t, "Read", post[0]["matcher"])
	assert.Contains(t, post[0]["command"], "hook report --agent cursor")
	assert.Contains(t, post[0]["command"], bin)
	start := cursorEntries(t, path, "sessionStart")
	require.Len(t, start, 1)
	assert.Contains(t, start[0]["command"], "hook autosync")
	assert.NotContains(t, read(t, path), "skael-cursor-stop.sh")
	assert.Contains(t, read(t, path), "mine.sh")

	require.NoError(t, hooks.UninstallForAgent("cursor", path))
	require.NoError(t, hooks.UninstallAutoSyncForAgent("cursor", path))
	assert.NotContains(t, read(t, path), "skael")
	assert.Contains(t, read(t, path), "mine.sh")
}

func TestPlugins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugins", "skael-tracking.ts")
	require.NoError(t, hooks.InstallForAgent("opencode", path, bin))
	require.NoError(t, hooks.InstallAutoSyncForAgent("opencode", path, bin))
	assert.NotContains(t, read(t, path), "api_key\":", "no credential is written into a plugin")
	auto := read(t, filepath.Join(dir, "plugins", "skael-autosync.ts"))
	assert.Contains(t, auto, `spawn("`+bin+`", ["hook","autosync"]`)
	assert.NotContains(t, auto, "bash")
	assert.True(t, hooks.State("opencode", path, bin).ActivationCurrent)

	require.NoError(t, hooks.UninstallForAgent("opencode", path))
	require.NoError(t, hooks.UninstallAutoSyncForAgent("opencode", path))
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
	require.NoError(t, hooks.UninstallForAgent("opencode", path), "a missing plugin is not an error")
}

// A hook that names a deleted script reads as out of date, and repair
// rewrites it to the binary. A hand-formatted current file stays current.
func TestState_LegacyAndCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"hooks":{"PreToolUse":[{"matcher":"Skill","hooks":[{"type":"command","command":"SKAEL_AGENT=claude-code /h/.skael/hooks/skael-hook.sh","_managed_by":"skael"}]}]}}`
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0o644))

	s := hooks.State("claude-code", path, bin)
	assert.True(t, s.Activation)
	assert.False(t, s.ActivationCurrent)
	assert.False(t, s.AutoSync)

	require.NoError(t, hooks.Repair("claude-code", path, bin))
	assert.NotContains(t, read(t, path), "skael-hook.sh")
	assert.True(t, hooks.State("claude-code", path, bin).ActivationCurrent)
	assert.False(t, hooks.State("claude-code", path, "/other/skael").ActivationCurrent)

	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(read(t, path)), &m))
	compact, _ := json.Marshal(m)
	require.NoError(t, os.WriteFile(path, compact, 0o644))
	assert.True(t, hooks.State("claude-code", path, bin).ActivationCurrent)
}
