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
