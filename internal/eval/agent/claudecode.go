package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/internal/eval/lint"
)

// ClaudeCode adapts the Claude Code CLI.
type ClaudeCode struct{}

// New returns a Claude Code adapter.
func New() *ClaudeCode { return &ClaudeCode{} }

// Name identifies the adapter in reports and panel matrices.
func (a *ClaudeCode) Name() string { return "claude-code" }

// Caps reports Claude Code's capabilities.
func (a *ClaudeCode) Caps() Caps {
	return Caps{
		EventTier:               "A",
		ModelFlag:               "--model",
		SkillDir:                ".claude/skills",
		AuthDirs:                []string{"~/.claude", "~/.config/claude"},
		AuthEnv:                 []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"},
		SupportsSkillInvocation: true,
	}
}

// InstallSkill copies a skill bundle into the workspace's project-local
// skill directory.
func (a *ClaudeCode) InstallSkill(workspace, bundlePath string) error {
	dst := filepath.Join(workspace, a.Caps().SkillDir, filepath.Base(bundlePath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("claudecode.InstallSkill mkdir: %w", err)
	}
	if err := copyTree(bundlePath, dst); err != nil {
		return fmt.Errorf("claudecode.InstallSkill copy: %w", err)
	}
	return nil
}

// copyTree copies a directory tree, refusing symlinks and filtering through
// lint.Excluded so the eval sidecar (oracle scripts, verifiers) never lands
// in the workspace of the agent being measured.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel != "." && lint.Excluded(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)

		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return fmt.Errorf("refusing to copy non-regular file %s", rel)
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
