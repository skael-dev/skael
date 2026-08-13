package scan

import "regexp"

// Rule defines a single security detection rule with its pattern and metadata.
type Rule struct {
	Name       string
	Category   string
	Severity   string
	Confidence string
	Pattern    *regexp.Regexp
	Message    string
	// Reject suppresses a match when the text also matches this. RE2 has no
	// lookahead, so this is how a rule excludes placeholders.
	Reject *regexp.Regexp
	// Class overrides the class derived from Category. Every override must
	// carry a comment saying why, because nothing else verifies correctness.
	Class Class
}

// ResolvedClass returns the rule's explicit Class override, or its category's.
func (r Rule) ResolvedClass() (Class, bool) {
	if r.Class != "" {
		return r.Class, true
	}
	return ClassOf(r.Category)
}

// AllRules returns every native detection rule.
func AllRules() []Rule {
	var rules []Rule
	rules = append(rules, secretRules...)
	rules = append(rules, injectionRules...)
	rules = append(rules, exfiltrationRules...)
	rules = append(rules, obfuscationRules...)
	rules = append(rules, executionRules...)
	return rules
}
