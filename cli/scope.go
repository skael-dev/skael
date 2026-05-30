package cli

import (
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/cli/agents"
)

// Scope controls where synced skills are placed.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

// resolveScope applies precedence: flag > config > default(project).
// Empty strings mean "unset".
func resolveScope(flagVal, configVal string) Scope {
	switch {
	case flagVal != "":
		return Scope(flagVal)
	case configVal != "":
		return Scope(configVal)
	default:
		return ScopeProject
	}
}

// validScope reports whether s is a recognised scope value.
func validScope(s string) bool {
	return s == string(ScopeProject) || s == string(ScopeUser)
}

// gitRoot returns the nearest ancestor of start containing a .git entry,
// or start itself if none is found (no external git dependency).
func gitRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

// agentSkillsBase returns the base skills directory for an agent under the
// given scope (without the skill-name leaf).
func agentSkillsBase(a agents.Agent, scope Scope, home, root string) string {
	if scope == ScopeProject {
		return a.ProjectSkillsDir(root)
	}
	return a.UserSkillsDir(home)
}
