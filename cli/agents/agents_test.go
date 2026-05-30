package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect_ClaudeCode(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o755))

	detected := DetectIn(home)

	require.Len(t, detected, 1)
	assert.Equal(t, "claude-code", detected[0].Name())
	assert.Equal(t, filepath.Join(home, ".claude", "skills"), detected[0].SkillsDir(home))
}

func TestDetect_Codex(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	require.NoError(t, os.MkdirAll(codexDir, 0o755))

	detected := DetectIn(home)

	require.Len(t, detected, 1)
	assert.Equal(t, "codex", detected[0].Name())
}

func TestDetect_NoneInstalled(t *testing.T) {
	home := t.TempDir()

	detected := DetectIn(home)

	assert.Len(t, detected, 0)
}

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

func TestGlobalAgents_NotProjectScoped(t *testing.T) {
	agents := []Agent{&ClaudeCode{}, &Codex{}, &OpenCode{}}
	for _, a := range agents {
		assert.False(t, a.ProjectScoped(), "%s must not be project-scoped", a.Name())
	}
}

func TestSkillsDirs_UserAndProject(t *testing.T) {
	home := "/home/u"
	root := "/repo"
	cases := []struct {
		agent       Agent
		wantUser    string
		wantProject string
	}{
		{&ClaudeCode{}, "/home/u/.claude/skills", "/repo/.claude/skills"},
		{&Codex{}, "/home/u/.codex/skills", "/repo/.agents/skills"},
		{&OpenCode{}, "/home/u/.config/opencode/skills", "/repo/.opencode/skills"},
		{&Cursor{}, "/home/u/.cursor/skills", "/repo/.cursor/skills"},
	}
	for _, c := range cases {
		assert.Equal(t, c.wantUser, c.agent.UserSkillsDir(home), "%s user", c.agent.Name())
		assert.Equal(t, c.wantProject, c.agent.ProjectSkillsDir(root), "%s project", c.agent.Name())
	}
}
