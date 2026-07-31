package contract

import (
	"errors"
	"fmt"
	stdpath "path"
	"path/filepath"
	"strings"
)

// ErrBadPattern wraps every error MatchPath returns because pattern itself is
// malformed — a compiler defect. A caller must stop scoring rather than
// continue around it: the compiled contract cannot be trusted for any
// candidate once one of its patterns is invalid.
var ErrBadPattern = errors.New("contract: malformed pattern")

// ErrBadCandidate wraps every error MatchPath returns because candidate is
// unusable — an absolute path, a backslash-separated one — while pattern
// itself is fine. This is a recording defect, not a compiler defect: a
// caller should count the check as unevaluable and keep scoring the rest of
// the trajectory.
var ErrBadCandidate = errors.New("contract: unusable candidate")

// MatchPath reports whether candidate satisfies pattern, under the small glob
// dialect every Matcher.PathGlob and Matcher.PathNotGlob value in this
// package is written in. Consumers evaluating a compiled Contract against a
// trajectory must call MatchPath rather than path/filepath.Match directly:
// Go's standard library treats "**" as two ordinary, non-recursive stars —
// neither crosses a "/" — so filepath.Match("out/**", "out/sub/file.csv")
// returns false. A forbid rule meaning "nothing outside out/" compiled to
// PathNotGlob "out/**" would then flag a legitimate nested write as a
// violation. MatchPath defines "/**" to mean "any path under here, at any
// depth" instead.
//
// The full dialect, every behaviour it commits to, and the reasoning behind
// each — so that a caller can predict MatchPath's result for any input from
// this comment alone, without reading the implementation:
//
//   - A trailing "/**" matches recursively, at any depth: "out/**" matches
//     "out/tables.csv", "out/tables/q1.csv", and "out/a/b/c.csv" alike.
//   - "out/**" does NOT match the bare directory "out" itself. "/**" names
//     what is inside a directory, not the directory. A rule that must also
//     forbid the bare directory has to say so with a second, explicit
//     pattern — this keeps "**" meaning exactly one thing.
//   - A single "*" keeps its ordinary, non-recursive meaning and never
//     crosses a "/": "out/*" matches "out/tables.csv" but not
//     "out/tables/q1.csv". Only a trailing "/**" is recursive; a bare "*"
//     is not made recursive by this package.
//   - "**" is only meaningful as a whole path segment, and only as the
//     pattern's final segment, immediately after a "/". Any other use —
//     "a/**/b", "**/a", "a**b", a bare "**" with no preceding segment — is a
//     malformed pattern and returns an error. A malformed pattern must not
//     silently return false: a forbid rule that silently never matches is
//     the inert-rule problem in a new disguise.
//   - pattern is matched segment-wise exactly as written: unlike candidate,
//     it is never passed through path.Clean. A ".." segment in pattern is
//     therefore rejected as malformed, with its own distinct error, rather
//     than compared literally against a candidate that path.Clean will
//     essentially never render as a literal "..": a pattern segment of ".."
//     would otherwise never match anything, permanently and silently — the
//     same inert-rule failure the candidate-side normalization exists to
//     prevent, just relocated to the other argument. Patterns are produced
//     by this package's own compiler, not by comparing untrusted text, so
//     this is a defensive rejection of a compiler bug, not a security
//     boundary the way the candidate-side checks are.
//   - candidate is lexically normalized with path.Clean before matching, so
//     "." and ".." segments are resolved rather than compared literally: a
//     leading "./" is stripped ("./out/x.csv" behaves as "out/x.csv"), and
//     "out/../etc/passwd" is recognised as "etc/passwd" — outside out/, not
//     inside it. Without this, a MUST-NOT scoped to "out/" would let a
//     traversal write escape undetected: the worse of the two directions
//     this package can get wrong, since a missed violation looks like a
//     clean run rather than a run worth investigating. A path that still
//     escapes its own relative root after normalizing — "../x",
//     "out/../../x" — can never satisfy a relative pattern, so MatchPath
//     reports no match: a MUST-NOT scoped to a subdirectory correctly flags
//     it, and a MUST/step matcher correctly refuses to count it as done.
//   - The dialect is POSIX ("/"-separated) only. A pattern or candidate
//     containing a literal backslash is a reported error, never a silent
//     false: this repository cross-compiles for windows/amd64, and a
//     Windows-recorded trajectory's "\"-separated paths would otherwise
//     fail every path rule silently — every forbid rule inert, every path
//     step unsatisfied, and the resulting score merely wrong rather than
//     visibly broken. Whichever component records or replays a Windows
//     trajectory is responsible for converting to "/" first.
//   - An absolute pattern ("/out/**") is a reported error, for the same
//     reason a ".." segment is: every pattern this package compiles is
//     workspace-relative, so an absolute one can never match any candidate.
//     Silently returning false would make the rule inert — every write
//     allowed, or every step unsatisfied — and look like a badly-behaved
//     skill rather than a compiler bug.
//   - An absolute candidate ("/etc/passwd") is a reported error, not a
//     quiet false. Every path this package's compiled patterns describe is
//     workspace-relative, so an absolute candidate reaching MatchPath means
//     something upstream already lost the workspace root; silently
//     comparing it against a relative pattern would almost always report
//     "no match" for the wrong reason — worth surfacing loudly rather than
//     folding into the same "false" a legitimate out-of-scope path returns.
//   - An empty pattern matches only a candidate that normalizes to the
//     empty string — which none do, since path.Clean("") is "." and
//     path.Clean never produces "" for any input. So in practice
//     MatchPath("", candidate) is false for every candidate MatchPath
//     accepts, including MatchPath("", ""). An empty candidate normalizes
//     to "." (the current directory) and matches only a pattern that
//     itself resolves to exactly ".".
//   - Single-segment matching — the plain (non-"/**") case, and each fixed
//     prefix segment of a recursive pattern — is filepath.Match underneath,
//     unchanged. Its metacharacters ("*", "?", and a "[...]" character
//     class) stay live in every segment. A pattern segment containing a
//     literal "[" or "]" is parsed as a character class, not a literal
//     bracket, so a real path segment containing the same literal bracket
//     text will not match it unless the shapes coincide by accident (e.g.
//     pattern segment "a.b[1]" matches path segment "a.b1", not "a.b[1]").
//     This is inherited from filepath.Match unchanged, not something this
//     package attempts to fix.
func MatchPath(pattern, candidate string) (bool, error) {
	segments := strings.Split(pattern, "/")
	for i, seg := range segments {
		if seg == ".." {
			return false, fmt.Errorf("%w %q: a %q segment is not allowed; the pattern side is matched as written, without \"..\" normalization", ErrBadPattern, pattern, "..")
		}
		if !strings.Contains(seg, "**") {
			continue
		}
		if seg != "**" {
			return false, fmt.Errorf("%w %q: %q must be a whole path segment, not part of one", ErrBadPattern, pattern, "**")
		}
		if i != len(segments)-1 {
			return false, fmt.Errorf("%w %q: %q is only meaningful as the final segment", ErrBadPattern, pattern, "**")
		}
		if len(segments) == 1 {
			return false, fmt.Errorf("%w %q: %q needs a preceding path", ErrBadPattern, pattern, "**")
		}
	}

	if strings.HasPrefix(pattern, "/") {
		return false, fmt.Errorf("%w %q: an absolute pattern is not allowed; MatchPath compares workspace-relative paths only", ErrBadPattern, pattern)
	}

	if strings.Contains(pattern, `\`) {
		return false, fmt.Errorf("%w: pattern %q contains a backslash; this package's patterns are POSIX-style (\"/\"-separated) only", ErrBadPattern, pattern)
	}
	if strings.Contains(candidate, `\`) {
		return false, fmt.Errorf("%w: path %q contains a backslash; this package compares POSIX-style (\"/\"-separated) paths only", ErrBadCandidate, candidate)
	}
	if strings.HasPrefix(candidate, "/") {
		return false, fmt.Errorf("%w: path %q is absolute; MatchPath compares workspace-relative paths only", ErrBadCandidate, candidate)
	}

	// Resolve "." and ".." lexically before matching, so a traversal like
	// "out/../etc/passwd" is compared as "etc/passwd" rather than by its
	// literal, misleading text.
	clean := stdpath.Clean(candidate)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		// Escapes its own relative root entirely: cannot satisfy any
		// relative pattern this package compiles.
		return false, nil
	}

	if segments[len(segments)-1] == "**" {
		prefix := segments[:len(segments)-1]
		cleanSegments := strings.Split(clean, "/")
		if len(cleanSegments) <= len(prefix) {
			// Either fewer segments than the prefix (can't match), or exactly
			// the prefix's length — the bare directory itself, which "/**"
			// deliberately does not match.
			return false, nil
		}
		for i, seg := range prefix {
			ok, err := filepath.Match(seg, cleanSegments[i])
			if err != nil {
				return false, fmt.Errorf("%w %q: %v", ErrBadPattern, pattern, err)
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}

	ok, err := filepath.Match(pattern, clean)
	if err != nil {
		return false, fmt.Errorf("%w %q: %v", ErrBadPattern, pattern, err)
	}
	return ok, nil
}
