// Package claudecode adapts the Claude Code CLI. Flags and stream shapes are
// isolated here: CLI churn is a known risk, so every version-sensitive detail
// lives in this package and is pinned by recorded fixtures in testdata/.
package claudecode

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/internal/eval/agent"
)

// Adapter implements agent.Adapter for Claude Code.
type Adapter struct{}

// New returns a Claude Code adapter.
func New() *Adapter { return &Adapter{} }

// Name identifies the adapter in reports and panel matrices.
func (a *Adapter) Name() string { return "claude-code" }

// Caps reports Claude Code's capabilities. Verified against CLI 2.1.220: the
// stream reports individual tool calls and results (tier A) and exposes skill
// invocation as an explicit Skill tool call.
func (a *Adapter) Caps() agent.Caps {
	return agent.Caps{
		EventTier:               "A",
		ModelFlag:               "--model",
		SkillDir:                ".claude/skills",
		AuthDirs:                []string{"~/.claude", "~/.config/claude"},
		SupportsSkillInvocation: true,
	}
}

// InstallSkill copies a skill bundle into the workspace's project-local skill
// directory. Project-local rather than user-level install keeps each run's
// visible skill set exactly what the run intends.
func (a *Adapter) InstallSkill(workspace, bundlePath string) error {
	dst := filepath.Join(workspace, a.Caps().SkillDir, filepath.Base(bundlePath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("claudecode.InstallSkill mkdir: %w", err)
	}
	if err := copyTree(bundlePath, dst); err != nil {
		return fmt.Errorf("claudecode.InstallSkill copy: %w", err)
	}
	return nil
}

// Invoke runs a headless session. Implemented with the sandbox.
func (a *Adapter) Invoke(context.Context, agent.InvokeSpec) (agent.RawStream, error) {
	return nil, agent.ErrInvokeNotImplemented
}

// copyTree copies a directory tree, refusing symlinks. A skill bundle is
// untrusted input, and a symlink in it would escape the workspace.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
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
