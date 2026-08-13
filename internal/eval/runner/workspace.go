package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/skael-dev/skael/internal/eval/suite"
)

const (
	wsDirMode  = os.FileMode(0o755)
	wsFileMode = os.FileMode(0o644)
)

// stageEvalWorkspace creates a workspace and copies the eval's input files.
// evals.json is never staged — it holds the answer key.
func stageEvalWorkspace(suiteDir string, ev suite.Eval, root string) (_ string, err error) {
	ws, err := os.MkdirTemp(root, "skael-run-*")
	if err != nil {
		return "", fmt.Errorf("runner: creating workspace: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(ws)
		}
	}()

	for _, rel := range ev.Files {
		src := filepath.Join(suiteDir, filepath.FromSlash(rel))
		b, rErr := os.ReadFile(src)
		if rErr != nil {
			return "", fmt.Errorf("runner: staging input file %q for eval %d: %w", rel, ev.ID, rErr)
		}
		if err := os.WriteFile(filepath.Join(ws, filepath.Base(rel)), b, wsFileMode); err != nil {
			return "", fmt.Errorf("runner: staging input file %q for eval %d: %w", rel, ev.ID, err)
		}
	}
	return ws, nil
}

func stageProbeWorkspace() (string, error) {
	ws, err := os.MkdirTemp("", "skael-probe-*")
	if err != nil {
		return "", fmt.Errorf("runner: creating probe workspace: %w", err)
	}
	return ws, nil
}

type distractorFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

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
