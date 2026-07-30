package lint_test

import (
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
