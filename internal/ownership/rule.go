// Package ownership resolves who owns a skill name and who may manage a
// pattern. Pure functions over a rule set — no database, no context, no clock.
package ownership

import (
	"fmt"
	"regexp"
	"strings"
)

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

// Rule is one ownership assignment.
type Rule struct {
	ID      string   `json:"id"`
	Pattern string   `json:"pattern"`
	Members []string `json:"members"` // user IDs
}

// Resolution is the outcome of matching a name against a rule set.
type Resolution struct {
	Rule    *Rule
	Members []string
}

func (r Resolution) Unowned() bool { return r.Rule == nil }

func (r Resolution) Contains(userID string) bool {
	for _, m := range r.Members {
		if m == userID {
			return true
		}
	}
	return false
}

func IsPrefix(p string) bool { return strings.HasSuffix(p, "*") }

// Scope returns the prefix a name must begin with to match p.
func Scope(p string) string {
	if !IsPrefix(p) {
		return p
	}
	return strings.TrimSuffix(p, "*")
}

// ValidatePattern enforces the grammar: an exact skill name, a prefix ending
// in "*", or the bare "*".
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
	base = strings.TrimSuffix(base, ":")
	if base == "" {
		return fmt.Errorf("ownership: pattern %q has no name part", p)
	}
	if !nameRe.MatchString(base) {
		return fmt.Errorf("ownership: pattern %q is not a valid skill name or namespace", p)
	}
	return nil
}

func Matches(pattern, name string) bool {
	if !IsPrefix(pattern) {
		return pattern == name
	}
	return strings.HasPrefix(name, Scope(pattern))
}

// Resolve picks the single rule that governs name: exact match, then longest
// prefix, then unowned. Longest match replaces rather than stacks.
func Resolve(name string, rules []Rule) Resolution {
	var best *Rule
	bestLen := -1

	for i := range rules {
		r := &rules[i]
		if !Matches(r.Pattern, name) {
			continue
		}
		if !IsPrefix(r.Pattern) {
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
