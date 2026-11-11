package agents

import "path/filepath"

// Pi represents the pi coding agent. It reads skills from .agents/skills, and
// skael hooks into it through extensions in ~/.pi/agent/extensions.
type Pi struct{}

func (p *Pi) Name() string { return "pi" }

func (p *Pi) UserSkillsDir(home string) string {
	return filepath.Join(home, ".pi", "agent", "skills")
}

func (p *Pi) ProjectSkillsDir(root string) string {
	return filepath.Join(root, ".pi", "skills")
}

func (p *Pi) ConfigPath(home string) string {
	return filepath.Join(home, ".pi", "agent", "extensions", "skael-tracking.ts")
}

func (p *Pi) Detected(home string) bool {
	return dirExists(filepath.Join(home, ".pi", "agent"))
}
