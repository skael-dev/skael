package agents

import "path/filepath"

// ClaudeCode represents the Claude Code agent by Anthropic.
type ClaudeCode struct{}

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) SkillsDir(home string) string {
	return filepath.Join(home, ".claude", "skills")
}

func (c *ClaudeCode) UserSkillsDir(home string) string {
	return filepath.Join(home, ".claude", "skills")
}

func (c *ClaudeCode) ProjectSkillsDir(root string) string {
	return filepath.Join(root, ".claude", "skills")
}

func (c *ClaudeCode) ConfigPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

func (c *ClaudeCode) Detected(home string) bool {
	return dirExists(filepath.Join(home, ".claude"))
}

func (c *ClaudeCode) ProjectScoped() bool { return false }
