package contract

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MatchPath reports whether path satisfies pattern, under the small glob
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
// The dialect, and the choices this implementation makes explicit:
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
//   - A leading "./" on path is stripped before matching, so "./out/x.csv"
//     and "out/x.csv" are equivalent. Patterns are not similarly normalized:
//     this package never compiles a pattern with a leading "./".
//   - "**" is only meaningful as a whole path segment, and only as the
//     pattern's final segment, immediately after a "/". Any other use —
//     "a/**/b", "**/a", "a**b", a bare "**" with no preceding segment — is a
//     malformed pattern and returns an error. A malformed pattern must not
//     silently return false: a forbid rule that silently never matches is
//     the inert-rule problem in a new disguise.
func MatchPath(pattern, path string) (bool, error) {
	segments := strings.Split(pattern, "/")
	for i, seg := range segments {
		if !strings.Contains(seg, "**") {
			continue
		}
		if seg != "**" {
			return false, fmt.Errorf("contract: malformed pattern %q: %q must be a whole path segment, not part of one", pattern, "**")
		}
		if i != len(segments)-1 {
			return false, fmt.Errorf("contract: malformed pattern %q: %q is only meaningful as the final segment", pattern, "**")
		}
	}

	path = strings.TrimPrefix(path, "./")

	if segments[len(segments)-1] == "**" {
		prefix := segments[:len(segments)-1]
		if len(prefix) == 0 {
			return false, fmt.Errorf("contract: malformed pattern %q: %q needs a preceding path", pattern, "**")
		}

		pathSegments := strings.Split(path, "/")
		if len(pathSegments) <= len(prefix) {
			// Either fewer segments than the prefix (can't match), or exactly
			// the prefix's length — the bare directory itself, which "/**"
			// deliberately does not match.
			return false, nil
		}
		for i, seg := range prefix {
			ok, err := filepath.Match(seg, pathSegments[i])
			if err != nil {
				return false, fmt.Errorf("contract: pattern %q: %w", pattern, err)
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}

	ok, err := filepath.Match(pattern, path)
	if err != nil {
		return false, fmt.Errorf("contract: pattern %q: %w", pattern, err)
	}
	return ok, nil
}
