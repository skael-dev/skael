package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCursor_Detected(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)

	detected := DetectIn(home)
	assert.Len(t, detected, 1)
	assert.Equal(t, "cursor", detected[0].Name())
	assert.Equal(t, filepath.Join(home, ".cursor", "skills"), detected[0].SkillsDir(home))
	assert.Equal(t, filepath.Join(home, ".cursor", "hooks.json"), detected[0].ConfigPath(home))
}

func TestCursor_NotDetected(t *testing.T) {
	home := t.TempDir()
	c := &Cursor{}
	assert.False(t, c.Detected(home))
}

func TestCursor_ProjectScoped(t *testing.T) {
	c := &Cursor{}
	assert.True(t, c.ProjectScoped(), "cursor must be project-scoped")
}

func TestCursor_SkillsDir_ProjectRoot(t *testing.T) {
	projectRoot := "/tmp/my-project"
	c := &Cursor{}
	assert.Equal(t, filepath.Join(projectRoot, ".cursor", "skills"), c.SkillsDir(projectRoot))
}
