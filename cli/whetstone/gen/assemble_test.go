package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone/gen"
)

// TestRewriteDescription_ChangesOnlyTheFrontmatter pins that the tune command
// spends no generation pass and touches no prose. The body is what gen spent
// several model calls on.
func TestRewriteDescription_ChangesOnlyTheFrontmatter(t *testing.T) {
	dir := t.TempDir()
	original := "---\nname: pdf-extract\ndescription: old\n---\n\n# PDF Extract\n\nThe body.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := gen.RewriteDescription(dir, "a new description"); err != nil {
		t.Fatalf("RewriteDescription: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "description: a new description") {
		t.Errorf("the description was not rewritten:\n%s", got)
	}
	if !strings.Contains(string(got), "# PDF Extract\n\nThe body.") {
		t.Errorf("the body changed:\n%s", got)
	}
	if !strings.Contains(string(got), "name: pdf-extract") {
		t.Errorf("the name was lost:\n%s", got)
	}
}

// TestRewriteDescription_RefusesAFileWithNoFrontmatter stops a silent write
// that produces a SKILL.md no agent can read.
func TestRewriteDescription_RefusesAFileWithNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# No frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gen.RewriteDescription(dir, "x"); err == nil {
		t.Error("RewriteDescription accepted a SKILL.md with no frontmatter")
	}
}
