// Package ownership resolves who owns a skill name and who may manage a
// pattern. Pure functions over a rule set — no database, no context, no clock.
package ownership

import (
	"fmt"

	"github.com/skael-dev/skael/internal/pattern"
)

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

// IsPrefix reports whether p matches by prefix.
func IsPrefix(p string) bool { return pattern.IsPrefix(p) }

// Scope returns the prefix a name must begin with to match p.
func Scope(p string) string { return pattern.Scope(p) }

// ValidatePattern enforces the shared pattern grammar.
func ValidatePattern(p string) error {
	if err := pattern.Validate(p); err != nil {
		return fmt.Errorf("ownership: %w", err)
	}
	return nil
}

// Matches reports whether name falls under the rule's pattern.
func Matches(p, name string) bool { return pattern.Matches(p, name) }

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
