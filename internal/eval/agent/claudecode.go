package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/internal/eval/lint"
)

// ClaudeCode adapts the Claude Code CLI. Flags and stream shapes are isolated
// in claudecode.go, invoke.go and parse.go: CLI churn is a known risk, so every
// version-sensitive detail is pinned by recorded fixtures in testdata/.
type ClaudeCode struct{}

// New returns a Claude Code adapter.
func New() *ClaudeCode { return &ClaudeCode{} }

// Name identifies the adapter in reports and panel matrices.
func (a *ClaudeCode) Name() string { return "claude-code" }

// Caps reports Claude Code's capabilities. Verified against CLI 2.1.220: the
// stream reports individual tool calls and results (tier A) and exposes skill
// invocation as an explicit Skill tool call.
func (a *ClaudeCode) Caps() Caps {
	return Caps{
		EventTier: "A",
		ModelFlag: "--model",
		SkillDir:  ".claude/skills",
		AuthDirs:  []string{"~/.claude", "~/.config/claude"},
		// ANTHROPIC_API_KEY is priority 3 in Claude Code's auth order and is
		// always used in non-interactive -p mode; CLAUDE_CODE_OAUTH_TOKEN
		// (from `claude setup-token`) is the subscription equivalent. Both are
		// documented and verified against the CLI. ANTHROPIC_BASE_URL and
		// ANTHROPIC_AUTH_TOKEN are also read by the CLI, and together are what
		// points this panel agent at an Anthropic-compatible gateway such as
		// OpenRouter instead of Anthropic's own API. Adding them here is safe
		// for existing users: both are simply unset unless the worker's own
		// environment sets them.
		AuthEnv:                 []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"},
		SupportsSkillInvocation: true,
	}
}

// InstallSkill copies a skill bundle into the workspace's project-local skill
// directory. Project-local rather than user-level install keeps each run's
// visible skill set exactly what the run intends.
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

// copyTree copies a directory tree, refusing symlinks. A skill bundle is
// untrusted input, and a symlink in it would escape the workspace.
//
// It installs shipped content only. The directory handed to it is the
// authoring skill dir, which also holds the eval sidecar — including every
// task's oracle/solve.sh. Copying that in puts the reference solution inside
// the workspace of the agent being measured, defeating stageRunWorkspace's
// deliberate exclusion of it one layer up.
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
