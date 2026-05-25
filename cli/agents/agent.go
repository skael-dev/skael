package agents

import (
	"os"
)

// Agent represents an AI coding agent installed on the host machine.
type Agent interface {
	Name() string
	SkillsDir(homeDir string) string
	ConfigPath(homeDir string) string
	Detected(homeDir string) bool
}

// Detect returns all agents found using the real home directory.
func Detect() []Agent {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return DetectIn(home)
}

// DetectIn checks all known agents against homeDir and returns the ones detected.
func DetectIn(homeDir string) []Agent {
	known := []Agent{
		&ClaudeCode{},
		&Codex{},
		&OpenCode{},
		&Cursor{},
	}

	var found []Agent
	for _, a := range known {
		if a.Detected(homeDir) {
			found = append(found, a)
		}
	}
	return found
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
