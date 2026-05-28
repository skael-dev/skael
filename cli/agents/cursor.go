package agents

import "path/filepath"

// Cursor represents the Cursor IDE agent.
type Cursor struct{}

func (c *Cursor) Name() string { return "cursor" }

func (c *Cursor) SkillsDir(home string) string {
	return filepath.Join(home, ".cursor", "skills")
}

func (c *Cursor) ConfigPath(home string) string {
	return filepath.Join(home, ".cursor", "hooks.json")
}

func (c *Cursor) Detected(home string) bool {
	return dirExists(filepath.Join(home, ".cursor"))
}

func (c *Cursor) ProjectScoped() bool { return true }
