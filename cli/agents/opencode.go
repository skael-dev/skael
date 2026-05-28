package agents

import "path/filepath"

// OpenCode represents the OpenCode AI coding agent.
type OpenCode struct{}

func (o *OpenCode) Name() string { return "opencode" }

func (o *OpenCode) SkillsDir(home string) string {
	return filepath.Join(home, ".config", "opencode", "skills")
}

func (o *OpenCode) ConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "plugins", "skael-tracking.ts")
}

func (o *OpenCode) Detected(home string) bool {
	return dirExists(filepath.Join(home, ".config", "opencode"))
}

func (o *OpenCode) ProjectScoped() bool { return false }
