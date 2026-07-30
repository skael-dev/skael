package whetstone_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone"
)

// cleanSkillMD is a bundle that lints clean: a frontmatter name matching the
// directory, a description carrying trigger language, numbered steps with
// postconditions, and a terminal fallback line.
const cleanSkillMD = `---
name: pdf-extract
description: Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction.
license: Apache-2.0
---

# PDF Extract

1. Run ` + "`scripts/extract.py <input.pdf>`" + `. Postcondition: out/tables.csv exists.
2. Run ` + "`scripts/validate.py out/tables.csv`" + `. Postcondition: exits 0.

If a checkpoint cannot be satisfied after one retry, stop and report state.
`

// warnOnlySkillMD is spec-valid but omits the terminal fallback line, which
// lint reports as a warning rather than an error. It is what distinguishes
// --strict from the default exit code.
const warnOnlySkillMD = `---
name: pdf-extract
description: Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction.
license: Apache-2.0
---

# PDF Extract

1. Run ` + "`scripts/extract.py <input.pdf>`" + `. Postcondition: out/tables.csv exists.
2. Run ` + "`scripts/validate.py out/tables.csv`" + `. Postcondition: exits 0.
`

func TestRunLint_ExitCodeReflectsErrors(t *testing.T) {
	clean := writeSkill(t, "pdf-extract", cleanSkillMD)
	code, err := whetstone.RunLint(clean, false)
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if code != 0 {
		t.Errorf("clean bundle exit code = %d, want 0", code)
	}

	broken := writeSkill(t, "pdf-extract", "---\nname: wrong-name\n---\n\n# x\n")
	code, err = whetstone.RunLint(broken, false)
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if code != 1 {
		t.Errorf("broken bundle exit code = %d, want 1", code)
	}
}

func TestRunLint_StrictPromotesWarningsToErrors(t *testing.T) {
	// A bundle with warnings but no errors: exit 0 normally, 1 under --strict.
	dir := writeSkill(t, "pdf-extract", warnOnlySkillMD)

	code, err := whetstone.RunLint(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("non-strict exit code = %d, want 0 (this bundle must warn, not error)", code)
	}

	code, err = whetstone.RunLint(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("strict exit code = %d, want 1", code)
	}
}

// TestRunLint_StrictLeavesACleanBundleAlone pins the other half of --strict:
// promoting warnings must not turn a bundle with no findings at all into a
// failure, or the flag is unusable in CI.
func TestRunLint_StrictLeavesACleanBundleAlone(t *testing.T) {
	dir := writeSkill(t, "pdf-extract", cleanSkillMD)

	code, err := whetstone.RunLint(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("strict exit code on a clean bundle = %d, want 0", code)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSkill(t *testing.T, name, skillMD string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	mustWrite(t, filepath.Join(dir, "SKILL.md"), skillMD)
	return dir
}
