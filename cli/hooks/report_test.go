package hooks_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/hooks"
)

func report(t *testing.T, dir, agent, payload string) *hooks.Event {
	t.Helper()
	return hooks.Report(agent, strings.NewReader(payload), dir)
}

// The rules the bash hook enforced: only a skill tool call with a valid skill
// name is an activation, and no other tool's name parameter counts.
func TestReport_SkillToolOnly(t *testing.T) {
	dir := t.TempDir()
	ev := report(t, dir, "claude-code", `{"tool_name":"Skill","tool_input":{"skill":"brainstorming"}}`)
	require.NotNil(t, ev)
	assert.Equal(t, "brainstorming", ev.SkillName)
	assert.Equal(t, "claude-code", ev.Agent)
	assert.Equal(t, "tool_invocation", ev.EventSource)
	assert.Len(t, ev.ProjectHash, 16)
	assert.Len(t, ev.DeveloperHash, 16)

	ev = report(t, dir, "codex", `{"tool_name":"skills_code-review","tool_input":{"skill":"skills_code-review"}}`)
	require.NotNil(t, ev)
	assert.Equal(t, "code-review", ev.SkillName)

	for _, p := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"ls"}}`,
		`{"tool_name":"Read","tool_input":{"name":"config.json"}}`,
		`{"tool_name":"apply_patch","tool_input":{"skill_name":"whatever"}}`,
		`{"tool":"apply_patch","tool_input":{"skill_name":"whatever"}}`,
		`{"tool_name":"Skill","tool_input":{"name":"whatever"}}`,
		`not json`,
	} {
		assert.Nil(t, report(t, dir, "codex", p), p)
	}
	for _, name := range []string{"../../etc/passwd", "Not A Skill", "trailing-", "", "null", strings.Repeat("a", 129)} {
		p := `{"tool_name":"Skill","tool_input":{"skill":` + strconv.Quote(name) + `}}`
		assert.Nil(t, report(t, dir, "claude-code", p), name)
	}
}

// The developer hash keeps the bash hook's input, so a developer's history
// stays one developer across the upgrade.
func TestReport_DeveloperHashMatchesTheBashHook(t *testing.T) {
	t.Setenv("USER", "ada")
	t.Setenv("HOSTNAME", "box")
	ev := report(t, t.TempDir(), "claude-code", `{"tool_name":"Skill","tool_input":{"skill":"x"}}`)
	require.NotNil(t, ev)
	// printf '%s' 'ada@box' | shasum -a 256 | cut -c1-16
	assert.Equal(t, "f758a64a1188f48e", ev.DeveloperHash)
	wd, _ := os.Getwd()
	assert.Equal(t, sha16(wd), ev.ProjectHash)
}

func TestReport_ModelAndVersion(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"skills":[{"name":"brainstorming","version":7}]}`), 0o600))
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte(
		`{"type":"assistant","message":{"model":"claude-opus-4-5"}}`+"\n"+
			`{"type":"assistant","message":{"model":"claude-sonnet-5"}}`+"\n"), 0o600))
	tp, _ := json.Marshal(transcript)

	ev := report(t, dir, "claude-code", `{"session_id":"s1","transcript_path":`+string(tp)+`,"tool_name":"Skill","tool_input":{"skill":"brainstorming"}}`)
	require.NotNil(t, ev)
	assert.Equal(t, "claude-sonnet-5", ev.Model)
	assert.Equal(t, 7, ev.Version)

	assert.Nil(t, report(t, dir, "claude-code", `{"hook_event_name":"SessionStart","session_id":"s2","model":"claude-haiku-4-5"}`))
	ev = report(t, dir, "claude-code", `{"session_id":"s2","tool_name":"Skill","tool_input":{"skill":"brainstorming"}}`)
	require.NotNil(t, ev)
	assert.Equal(t, "claude-haiku-4-5", ev.Model)

	assert.Nil(t, report(t, dir, "claude-code", `{"hook_event_name":"SessionStart","session_id":"../x","model":"m"}`))
	_, err := os.Stat(filepath.Join(dir, "x"))
	assert.True(t, os.IsNotExist(err), "a session id is never a path")
}

func writeSkill(t *testing.T, dir, frontmatterName string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	file := filepath.Join(dir, "SKILL.md")
	require.NoError(t, os.WriteFile(file, []byte("---\r\nname: \""+frontmatterName+"\"\r\ndescription: d\r\n---\r\nbody\r\n"), 0o644))
	return file
}

func cursorRead(t *testing.T, dir, file string) *hooks.Event {
	t.Helper()
	p, _ := json.Marshal(map[string]any{
		"hook_event_name": "postToolUse",
		"tool_name":       "Read",
		"tool_input":      map[string]any{"file_path": file},
		"model":           "default",
		"workspace_roots": []string{"/work/repo"},
	})
	return report(t, dir, "cursor", string(p))
}

// Cursor loads a skill by reading its SKILL.md inside a skills folder. A skael
// install is named by its folder; another skill by its frontmatter.
func TestReport_CursorRead(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"skills":[{"name":"platform:deploy","version":4}]}`), 0o600))
	root := t.TempDir()

	// Windows installs platform:deploy into platform_deploy.
	ev := cursorRead(t, dir, writeSkill(t, filepath.Join(root, ".agents", "skills", "platform_deploy"), "deploy"))
	require.NotNil(t, ev)
	assert.Equal(t, "platform:deploy", ev.SkillName)
	assert.Equal(t, 4, ev.Version)
	assert.Equal(t, "", ev.Model, "default is not a model")
	assert.Equal(t, sha16("/work/repo"), ev.ProjectHash)

	ev = cursorRead(t, t.TempDir(), writeSkill(t, filepath.Join(root, ".cursor", "skills", "deploy"), "platform:deploy"))
	require.NotNil(t, ev)
	assert.Equal(t, "platform:deploy", ev.SkillName)
	assert.Equal(t, 0, ev.Version)

	ev = cursorRead(t, dir, `C:\Users\ada\.cursor\skills\platform:deploy\SKILL.md`)
	require.NotNil(t, ev, "a Windows path matches the same skill roots")
	assert.Equal(t, "platform:deploy", ev.SkillName)

	for _, file := range []string{
		writeSkill(t, filepath.Join(root, "skills", "deploy"), "deploy"),
		writeSkill(t, filepath.Join(root, "docs"), "deploy"),
		writeSkill(t, filepath.Join(root, ".agents", "skills", "under_score"), "under_score"),
		filepath.Join(root, ".agents", "skills", "deploy", "README.md"),
	} {
		assert.Nil(t, cursorRead(t, dir, file), file)
	}
}

func TestAutoSyncDue(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	assert.True(t, hooks.AutoSyncDue(dir, now), "no state means never synced")
	write := func(last string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"last_sync":"`+last+`"}`), 0o600))
	}
	write("2026-11-13T11:45:00Z")
	assert.False(t, hooks.AutoSyncDue(dir, now))
	write("2026-11-13T11:30:00Z")
	assert.True(t, hooks.AutoSyncDue(dir, now))
	write("garbage")
	assert.True(t, hooks.AutoSyncDue(dir, now))
}

func sha16(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}
