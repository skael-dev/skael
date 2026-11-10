// Package pattern is the one grammar Skael uses to name a group of skills: an
// exact skill name, a prefix ending in "*" such as "payments:*", or "*" for
// every skill. Ownership rules, set entries and followed sources share it.
package pattern

import (
	"fmt"
	"regexp"
	"strings"
)

// NameRe is a valid skill name or namespace.
var NameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

// IsPrefix reports whether p matches by prefix rather than exactly.
func IsPrefix(p string) bool { return strings.HasSuffix(p, "*") }

// Scope returns the prefix a name must begin with to match p.
func Scope(p string) string {
	if !IsPrefix(p) {
		return p
	}
	return strings.TrimSuffix(p, "*")
}

// Validate enforces the grammar.
func Validate(p string) error {
	if p == "" {
		return fmt.Errorf("pattern is empty")
	}
	if p == "*" {
		return nil
	}
	if strings.Count(p, "*") > 1 {
		return fmt.Errorf("pattern %q has more than one %q", p, "*")
	}
	if i := strings.Index(p, "*"); i >= 0 && i != len(p)-1 {
		return fmt.Errorf("pattern %q may only use %q as the final character", p, "*")
	}
	base := strings.TrimSuffix(strings.TrimSuffix(p, "*"), ":")
	if base == "" {
		return fmt.Errorf("pattern %q has no name part", p)
	}
	if !NameRe.MatchString(base) {
		return fmt.Errorf("pattern %q is not a valid skill name or namespace", p)
	}
	return nil
}

// Matches reports whether name falls under p.
func Matches(p, name string) bool {
	if !IsPrefix(p) {
		return p == name
	}
	return strings.HasPrefix(name, Scope(p))
}
