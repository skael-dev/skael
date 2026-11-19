package skills_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/skills"
)

// The worked suite skael-evals ships is the one examples/code-review publishes
// with. Two copies that drift teach an agent a suite the docs do not run.
func TestEvals_WorkedSuiteMatchesTheExample(t *testing.T) {
	const embedded = "examples/code-review/evals"
	repo := filepath.Join("..", "examples", "code-review", "evals")
	seen := map[string]bool{}
	err := fs.WalkDir(skills.Evals, embedded, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(embedded, p)
		seen[rel] = true
		want, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Errorf("%s is in skael-evals but not in examples/code-review: %v", rel, err)
			return nil
		}
		got, _ := fs.ReadFile(skills.Evals, p)
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs between skael-evals and examples/code-review", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if rel, _ := filepath.Rel(repo, p); !seen[rel] {
				t.Errorf("%s is in examples/code-review but not in skael-evals", rel)
			}
		}
		return err
	})
}
