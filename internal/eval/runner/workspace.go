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

// stageEvalWorkspace creates a fresh session workspace for one eval run and
// copies that eval's input files into it, flattened to their base names.
//
// Only the files the eval lists are copied. evals.json itself is never staged:
// it holds the expectations, which are the answer key, and a workspace
// carrying it is not measuring the skill.
//
// root is Options.WorkspaceRoot: empty means os.TempDir(), and a non-empty
// value is a directory the caller has arranged to be visible at the same path
// to whatever daemon starts the sandbox containers.
func stageEvalWorkspace(suiteDir string, ev suite.Eval, root string) (_ string, err error) {
	// ws is deliberately a plain local, not the named return: an error return
	// below sets the named result to "", and a defer reading that instead of
	// this variable would call os.RemoveAll("") — a no-op — rather than
	// cleaning up the directory MkdirTemp actually created.
	//
	// MkdirTemp treats "" as os.TempDir(), so the default needs no branch.
	ws, err := os.MkdirTemp(root, "skael-run-*")
	if err != nil {
		return "", fmt.Errorf("runner: creating workspace: %w", err)
	}
	// Every return below this point leaves a workspace on disk unless it is
	// explicitly cleaned up; a caller only gets ws back on the final success
	// path, so any error return here must remove what MkdirTemp created.
	defer func() {
		if err != nil {
			_ = os.RemoveAll(ws)
		}
	}()

	for _, rel := range ev.Files {
		src := filepath.Join(suiteDir, filepath.FromSlash(rel))
		// suite.Validate has already refused a path that escapes the suite
		// directory, so a file that fails to open here is one the eval names
		// and the archive does not carry.
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
