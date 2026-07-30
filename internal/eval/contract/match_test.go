package contract_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
)

func TestMatchPath_RecursiveSuffixMatchesAnyDepth(t *testing.T) {
	// This is the case Go's stdlib filepath.Match gets wrong: "**" there is
	// two ordinary, non-recursive stars, so filepath.Match("out/**",
	// "out/tables/q1.csv") is false. MatchPath must return true.
	cases := []string{
		"out/tables.csv",
		"out/tables/q1.csv",
		"out/a/b/c.csv",
	}
	for _, path := range cases {
		matched, err := contract.MatchPath("out/**", path)
		if err != nil {
			t.Fatalf("MatchPath(%q, %q): %v", "out/**", path, err)
		}
		if !matched {
			t.Errorf("MatchPath(%q, %q) = false, want true", "out/**", path)
		}
	}
}

func TestMatchPath_RecursiveSuffixRejectsOtherTrees(t *testing.T) {
	matched, err := contract.MatchPath("out/**", "tmp/x.csv")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if matched {
		t.Error("MatchPath(\"out/**\", \"tmp/x.csv\") = true, want false")
	}
}

func TestMatchPath_RecursiveSuffixDoesNotMatchBareDirectory(t *testing.T) {
	// Documented decision: "out/**" names what is inside out/, not out/
	// itself. A rule needing to forbid the bare directory too must say so
	// with an explicit second pattern.
	matched, err := contract.MatchPath("out/**", "out")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if matched {
		t.Error(`MatchPath("out/**", "out") = true, want false (the bare directory is not "inside" it)`)
	}
}

func TestMatchPath_LeadingDotSlashOnPathIsNormalized(t *testing.T) {
	// Documented decision: a leading "./" on the candidate path is stripped
	// before matching, so "./out/x.csv" and "out/x.csv" are equivalent.
	matched, err := contract.MatchPath("out/**", "./out/tables/q1.csv")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if !matched {
		t.Error(`MatchPath("out/**", "./out/tables/q1.csv") = false, want true`)
	}
}

func TestMatchPath_SingleStarStaysNonRecursive(t *testing.T) {
	// A bare "*" must keep filepath.Match's ordinary meaning: it matches
	// within one path segment and never crosses a "/". Only a trailing
	// "/**" is recursive.
	matched, err := contract.MatchPath("out/*", "out/tables.csv")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if !matched {
		t.Error(`MatchPath("out/*", "out/tables.csv") = false, want true`)
	}

	matched, err = contract.MatchPath("out/*", "out/tables/q1.csv")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if matched {
		t.Error(`MatchPath("out/*", "out/tables/q1.csv") = true, want false (a single "*" must not cross "/")`)
	}
}

func TestMatchPath_MalformedPatternsAreErrors(t *testing.T) {
	// A malformed pattern must error, not silently return false — a forbid
	// rule that silently never matches is the inert-rule problem again.
	cases := []string{
		"a/**/b", // "**" not the final segment
		"**/a",   // "**" not the final segment
		"a**b",   // "**" not a whole segment
		"a***b",  // "**" not a whole segment (extra star)
		"**",     // "**" with no preceding path
	}
	for _, pattern := range cases {
		_, err := contract.MatchPath(pattern, "a/b/c")
		if err == nil {
			t.Errorf("MatchPath(%q, ...): want an error for a malformed pattern, got nil", pattern)
		}
	}
}
