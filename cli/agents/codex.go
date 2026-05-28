package agents

import "path/filepath"

// Codex represents the OpenAI Codex CLI agent.
type Codex struct{}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) SkillsDir(home string) string {
	return filepath.Join(home, ".codex", "skills")
}

func (c *Codex) ConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}

func (c *Codex) Detected(home string) bool {
	return dirExists(filepath.Join(home, ".codex"))
}

func (c *Codex) ProjectScoped() bool { return false }
