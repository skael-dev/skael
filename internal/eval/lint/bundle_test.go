package lint_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/lint"
)

// TestExcluded pins the one definition of what is not shipped skill content.
// pack consumes this predicate, so anything it gets wrong is a bundle that
// either fails a lint it should pass or ships scaffolding it should not.
func TestExcluded(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"eval", true},
		{"eval/contract.yaml", true},
		{"eval/suite/tasks/t1/oracle/solve.sh", true},
		{"spec.yaml", true},
		{"pdf-extract.tar.gz", true},
		{"SKILL.md", false},
		{"scripts/extract.py", false},
		{"references/format.md", false},
		// Anchored at the bundle root: these are ordinary shipped content and
		// must still be linted and scanned.
		{"references/eval/rubric.md", false},
		{"scripts/spec.yaml", false},
		{"assets/bundle.tar.gz", false},
		{"evaluation/notes.md", false},
		{"eval.md", false},
	}
	for _, tc := range cases {
		if got := lint.Excluded(tc.rel); got != tc.want {
			t.Errorf("Excluded(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// writeMinimalBundle writes a spec-compliant SKILL.md for name directly into
// dir, which must already exist. Unlike writeBundle (used elsewhere in this
// package), it does not create its own directory: these tests need to add a
// symlink or a stray file at a path they already hold a reference to.
func writeMinimalBundle(t *testing.T, dir, name string) {
	t.Helper()
	body := fmt.Sprintf("---\nname: %s\ndescription: Use when demoing a bundle in a lint test.\n---\n\n# Demo\n\n1. Do the thing. Postcondition: it is done.\n", name)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSymlinks_AreReportedEvenWhenTheirNameIsExcluded(t *testing.T) {
	dir := t.TempDir()
	writeMinimalBundle(t, dir, "demo")
	// A symlink named after excluded scaffolding was skipped before the
	// symlink test ran, so it was reported by nothing. Not exploitable — pack
	// skips non-regular files and skill.Unpack rejects symlinks outright — but
	// a bundle carrying one should say so.
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, lint.SidecarDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fs, err := lint.Conformance(dir)
	if err != nil {
		t.Fatalf("Conformance: %v", err)
	}
	if !hasRule(fs, "symlink-not-allowed") {
		t.Errorf("findings = %+v, want a symlink finding for an excluded-name symlink", fs)
	}
}

func TestConformance_ReportsARootTarball(t *testing.T) {
	dir := t.TempDir()
	writeMinimalBundle(t, dir, "demo")
	if err := os.WriteFile(filepath.Join(dir, "demo.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs, err := lint.Conformance(dir)
	if err != nil {
		t.Fatalf("Conformance: %v", err)
	}
	// It is silently dropped from pack's output as well as from lint. That is
	// what makes `pack .` idempotent and it stays — but an author genuinely
	// shipping a tarball deserves a diagnostic rather than a missing file.
	if !hasRule(fs, "bundle-artifact") {
		t.Errorf("findings = %+v, want a bundle-artifact warning", fs)
	}
}
