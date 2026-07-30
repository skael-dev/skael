package contract_test

import (
	"strings"
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

// TestMatchPath_DotDotInPatternIsAnError closes the pattern-side mirror of
// the candidate-side ".." fix: only candidate goes through path.Clean, so a
// ".." segment written directly into pattern would otherwise be compared
// literally against a cleaned candidate that essentially never contains a
// literal "..", making such a pattern permanently and silently inert —
// exactly the failure mode the candidate-side fix closed, just relocated to
// the other argument. A ".." segment in pattern must error instead.
func TestMatchPath_DotDotInPatternIsAnError(t *testing.T) {
	cases := []string{
		"out/../etc",
		"../out",
		"out/..",
	}
	for _, pattern := range cases {
		_, err := contract.MatchPath(pattern, "out/tables.csv")
		if err == nil {
			t.Errorf("MatchPath(%q, ...): want an error for a \"..\" segment in the pattern, got nil", pattern)
		}
	}
}

// TestMatchPath_ErrorsAreDistinguishable confirms a caller can tell which
// rule an invalid MatchPath call violated: the ".."-in-pattern, malformed-
// "**", and backslash errors must not read alike or collide on the same
// message, since a caller catching an error and trying to explain it to a
// user (or branch on it) needs the messages to actually differ.
func TestMatchPath_ErrorsAreDistinguishable(t *testing.T) {
	_, dotdotErr := contract.MatchPath("out/../etc", "out/tables.csv")
	_, starErr := contract.MatchPath("a**b", "a/b/c")
	_, backslashErr := contract.MatchPath(`out\tables.py`, "out/tables.csv")

	if dotdotErr == nil || starErr == nil || backslashErr == nil {
		t.Fatalf("expected all three calls to error: dotdot=%v star=%v backslash=%v", dotdotErr, starErr, backslashErr)
	}

	msgs := map[string]string{
		"dotdot":    dotdotErr.Error(),
		"star":      starErr.Error(),
		"backslash": backslashErr.Error(),
	}
	for name, msg := range msgs {
		for otherName, otherMsg := range msgs {
			if name == otherName {
				continue
			}
			if msg == otherMsg {
				t.Errorf("%s error and %s error are identical: %q", name, otherName, msg)
			}
		}
	}

	if !strings.Contains(msgs["dotdot"], "..") {
		t.Errorf("dotdot error %q does not name the offending %q", msgs["dotdot"], "..")
	}
	if !strings.Contains(msgs["star"], "**") {
		t.Errorf("star error %q does not name the offending %q", msgs["star"], "**")
	}
	if !strings.Contains(msgs["backslash"], "backslash") {
		t.Errorf("backslash error %q does not name the offending condition", msgs["backslash"])
	}
}

// TestMatchPath_DotDotIsResolvedBeforeMatching is the missed-violation fix:
// a traversal like "out/../etc/passwd" lexically resolves to "etc/passwd",
// which is outside "out/". Before this fix, MatchPath split the raw string
// on "/" without resolving "..", so "out/../etc/passwd" was compared
// segment-by-segment and its leading "out" segment matched the "out/**"
// prefix — reporting it as inside scope. That is worse than the recursive-
// suffix bug this package fixed earlier: a false violation gets found on
// investigation, a missed one — a misbehaving skill scoring clean — does
// not.
func TestMatchPath_DotDotIsResolvedBeforeMatching(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		wantMatched bool
	}{
		// The core fix: escapes out/ via traversal, must NOT match.
		{"traversal escapes scope", "out/../etc/passwd", false},
		// Stays legitimately inside out/ after resolving "a/..": must still
		// match, so normalizing ".." doesn't turn into over-eager rejection.
		{"traversal stays in scope", "out/a/../b.csv", true},
		// Escapes its own relative root entirely before even reaching "out".
		{"escapes relative root", "../x", false},
		// Same, via a compound traversal from inside out/.
		{"compound escape", "out/../../x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := contract.MatchPath("out/**", tc.path)
			if err != nil {
				t.Fatalf("MatchPath(%q, %q): %v", "out/**", tc.path, err)
			}
			if matched != tc.wantMatched {
				t.Errorf("MatchPath(%q, %q) = %v, want %v", "out/**", tc.path, matched, tc.wantMatched)
			}
		})
	}
}

// TestMatchPath_BackslashIsAnError pins the POSIX-only decision: a pattern
// or candidate containing a literal backslash is a reported error, never a
// silent false. A silent false here would mean a Windows-recorded
// trajectory's "\"-separated paths fail every path rule invisibly, since
// this repository cross-compiles for windows/amd64.
func TestMatchPath_BackslashIsAnError(t *testing.T) {
	if _, err := contract.MatchPath("out/**", `out\tables\q1.csv`); err == nil {
		t.Error(`MatchPath("out/**", "out\tables\q1.csv"): want an error, got nil`)
	}
	// A pattern with a backslash but no "**" at all, so this exercises only
	// the backslash check, not the (separately tested) malformed-"**" check
	// a pattern like "out\**" would also trip.
	if _, err := contract.MatchPath(`out\tables.py`, "out/tables.csv"); err == nil {
		t.Error(`MatchPath("out\tables.py", "out/tables.csv"): want an error, got nil`)
	}
}

// TestMatchPath_AbsolutePathIsAnError pins the decision that an absolute
// candidate is a reported error rather than a quiet false: every path this
// package's patterns describe is workspace-relative, so an absolute
// candidate means something upstream already lost the workspace root, which
// is worth surfacing rather than silently folding into an ordinary mismatch.
func TestMatchPath_AbsolutePathIsAnError(t *testing.T) {
	if _, err := contract.MatchPath("out/**", "/etc/passwd"); err == nil {
		t.Error(`MatchPath("out/**", "/etc/passwd"): want an error, got nil`)
	}
}

// TestMatchPath_EmptyPatternAndPath pins the documented (if slightly
// surprising) behaviour for empty inputs: path.Clean("") is ".", and
// path.Clean never produces "" for any input, so an empty pattern never
// matches any candidate MatchPath accepts — including MatchPath("", "").
// An empty candidate is treated as "." and matches only a pattern that
// itself resolves to exactly ".".
func TestMatchPath_EmptyPatternAndPath(t *testing.T) {
	matched, err := contract.MatchPath("", "")
	if err != nil {
		t.Fatalf(`MatchPath("", ""): %v`, err)
	}
	if matched {
		t.Error(`MatchPath("", "") = true, want false (an empty candidate normalizes to ".", which an empty pattern does not match)`)
	}

	matched, err = contract.MatchPath("out/**", "")
	if err != nil {
		t.Fatalf(`MatchPath("out/**", ""): %v`, err)
	}
	if matched {
		t.Error(`MatchPath("out/**", "") = true, want false (an empty candidate normalizes to ".", not something inside out/)`)
	}

	matched, err = contract.MatchPath(".", "")
	if err != nil {
		t.Fatalf(`MatchPath(".", ""): %v`, err)
	}
	if !matched {
		t.Error(`MatchPath(".", "") = false, want true (both sides normalize to ".")`)
	}
}

// TestMatchPath_LiteralBracketInPathSegment pins a known, inherited
// limitation rather than fixing it: single-segment matching is
// filepath.Match underneath, so "[" and "]" in a pattern segment are parsed
// as a character class, not literal brackets. A real path segment
// containing the same literal bracket text does not match its own literal
// text back.
func TestMatchPath_LiteralBracketInPathSegment(t *testing.T) {
	matched, err := contract.MatchPath("out/a.b[1]/x.csv", "out/a.b[1]/x.csv")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if matched {
		t.Error(`MatchPath("out/a.b[1]/x.csv", "out/a.b[1]/x.csv") = true, want false (documented filepath.Match-inherited limitation: "[1]" is a character class, not a literal bracket)`)
	}
}
