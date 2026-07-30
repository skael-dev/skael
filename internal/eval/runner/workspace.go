package runner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/skael-dev/skael/internal/eval/suite"
)

const (
	wsDirMode  = os.FileMode(0o755)
	wsFileMode = os.FileMode(0o644)
)

// stageRunWorkspace creates a fresh session workspace for one task run,
// copying only task.md and environment/ from taskDir. oracle/ and verifier/
// are deliberately never copied here: the oracle is the reference solution,
// and a workspace that carries it is not measuring the skill, it is handing
// the agent the answer. The verifier is mounted separately, after the
// session ends, so the agent cannot read it either.
func stageRunWorkspace(taskDir string) (string, error) {
	ws, err := os.MkdirTemp("", "skael-run-*")
	if err != nil {
		return "", fmt.Errorf("runner: creating workspace: %w", err)
	}

	promptPath := filepath.Join(taskDir, "task.md")
	b, err := os.ReadFile(promptPath)
	switch {
	case err == nil:
		if err := os.WriteFile(filepath.Join(ws, "task.md"), b, wsFileMode); err != nil {
			return "", fmt.Errorf("runner: staging task.md: %w", err)
		}
	case os.IsNotExist(err):
		// No task.md is a caller error surfaced elsewhere (a missing task),
		// not a reason to fail staging itself.
	default:
		return "", fmt.Errorf("runner: reading task.md: %w", err)
	}

	envDir := filepath.Join(taskDir, "environment")
	if info, err := os.Stat(envDir); err == nil && info.IsDir() {
		if err := copyTree(envDir, filepath.Join(ws, "environment")); err != nil {
			return "", fmt.Errorf("runner: staging environment: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("runner: staging environment: %w", err)
	}

	return ws, nil
}

// stageProbeWorkspace creates a bare workspace for a trigger probe or a
// health check — sessions with no task to stage.
func stageProbeWorkspace() (string, error) {
	ws, err := os.MkdirTemp("", "skael-probe-*")
	if err != nil {
		return "", fmt.Errorf("runner: creating probe workspace: %w", err)
	}
	return ws, nil
}

// copyTree copies a directory tree, refusing symlinks — the source ultimately
// traces back to a generated or model-authored suite, so it is untrusted the
// same way a skill bundle is.
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

		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("runner: refusing to stage symlink %q", p)
		}
		if d.IsDir() {
			return os.MkdirAll(target, wsDirMode)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, wsFileMode)
	})
}

// distractorFrontmatter is the minimal SKILL.md a distractor needs: enough
// for a trigger-precision measurement, which only checks whether the skill
// under test fired, never whether a distractor's body is well-formed.
type distractorFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// installDistractors writes each distractor as a SKILL.md with frontmatter
// only, alongside the skill under test. Trigger precision measured against no
// distractors measures nothing — the skill is the only candidate, so it
// always "wins".
func installDistractors(ws, skillDir string, ds []suite.Distractor) error {
	for _, d := range ds {
		dir := filepath.Join(ws, skillDir, d.Name)
		if err := os.MkdirAll(dir, wsDirMode); err != nil {
			return fmt.Errorf("runner: creating distractor directory for %q: %w", d.Name, err)
		}
		fm, err := yaml.Marshal(distractorFrontmatter{Name: d.Name, Description: d.Description})
		if err != nil {
			return fmt.Errorf("runner: marshalling distractor frontmatter for %q: %w", d.Name, err)
		}
		content := "---\n" + string(fm) + "---\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), wsFileMode); err != nil {
			return fmt.Errorf("runner: writing distractor SKILL.md for %q: %w", d.Name, err)
		}
	}
	return nil
}
