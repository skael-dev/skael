// Package ownership answers two questions and nothing else: who owns a skill
// name, and may this actor manage this pattern. Both are pure functions over
// a rule set — no database, no context, no clock — for the same reason
// internal/gate is pure: every policy question should be a table test.
package ownership

import (
	"fmt"
	"regexp"
	"strings"
)

// nameRe is the skill-name grammar, copied from internal/skill. A pattern is
// either a name matching it exactly, or such a name followed by ":*", or the
// bare "*".
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

// Rule is one ownership assignment: a pattern and the user IDs that own
// everything the pattern matches.
type Rule struct {
	ID      string   `json:"id"`
	Pattern string   `json:"pattern"`
	Members []string `json:"members"` // user IDs
}

// Resolution is the outcome of matching a name against a rule set. A nil Rule
// means unowned, which is a distinct state from "owned by nobody" — see O5.
type Resolution struct {
	Rule    *Rule
	Members []string
}

// Unowned reports whether no rule matched.
func (r Resolution) Unowned() bool { return r.Rule == nil }

// Contains reports whether userID is one of the resolved owners.
func (r Resolution) Contains(userID string) bool {
	for _, m := range r.Members {
		if m == userID {
			return true
		}
	}
	return false
}

// IsPrefix reports whether p is a prefix pattern (ends in "*") rather than an
// exact name.
func IsPrefix(p string) bool { return strings.HasSuffix(p, "*") }

// Scope returns the string every name in p's scope must begin with. For an
// exact pattern that is the pattern itself; for "payments:*" it is
// "payments:"; for the bare "*" it is "".
func Scope(p string) string {
	if !IsPrefix(p) {
		return p
	}
	return strings.TrimSuffix(p, "*")
}

// ValidatePattern enforces the grammar in the spec's §5: an exact skill name,
// or a prefix ending in a single trailing "*", or the bare "*". No mid-string
// globs, no character classes. A grammar that fits in your head is a grammar
// people trust.
func ValidatePattern(p string) error {
	if p == "" {
		return fmt.Errorf("ownership: pattern is empty")
	}
	if p == "*" {
		return nil
	}
	if strings.Count(p, "*") > 1 {
		return fmt.Errorf("ownership: pattern %q has more than one %q", p, "*")
	}
	if i := strings.Index(p, "*"); i >= 0 && i != len(p)-1 {
		return fmt.Errorf("ownership: pattern %q may only use %q as the final character", p, "*")
	}
	base := strings.TrimSuffix(p, "*")
	// A prefix pattern's base ends in the separator it delegates on, so strip
	// a single trailing ':' before validating the name half.
	base = strings.TrimSuffix(base, ":")
	if base == "" {
		return fmt.Errorf("ownership: pattern %q has no name part", p)
	}
	if !nameRe.MatchString(base) {
		return fmt.Errorf("ownership: pattern %q is not a valid skill name or namespace", p)
	}
	return nil
}

// Matches reports whether name falls in pattern's scope.
func Matches(pattern, name string) bool {
	if !IsPrefix(pattern) {
		return pattern == name
	}
	return strings.HasPrefix(name, Scope(pattern))
}

// Resolve picks the single rule that governs name: an exact match if one
// exists, otherwise the longest matching prefix, otherwise unowned.
//
// Longest match REPLACES rather than stacks — one rule applies, the same as
// CODEOWNERS and .gitignore. Stacking would mean a namespace owner could
// never delegate a skill away, and delegation is the whole point of patterns.
func Resolve(name string, rules []Rule) Resolution {
	var best *Rule
	bestLen := -1

	for i := range rules {
		r := &rules[i]
		if !Matches(r.Pattern, name) {
			continue
		}
		if !IsPrefix(r.Pattern) {
			// Exact match beats every prefix; nothing can be more specific.
			return Resolution{Rule: r, Members: r.Members}
		}
		if n := len(Scope(r.Pattern)); n > bestLen {
			best, bestLen = r, n
		}
	}

	if best == nil {
		return Resolution{}
	}
	return Resolution{Rule: best, Members: best.Members}
}
