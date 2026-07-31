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
	// Reject, when set, suppresses a match if the matched text also matches this
	// pattern. Go's regexp (RE2) has no lookahead, so this is how a rule excludes
	// placeholders/references (e.g. `password = "your-password-here"`) that would
	// otherwise be false positives.
	Reject *regexp.Regexp
}

// AllRules returns the concatenation of every native detection rule slice.
// scanner.go's init iterates this (rather than assembling its own copy) so the
// two cannot drift, and internal/gate's TestEveryRuleHasAClass uses it to
// guard that every rule's Category maps to a Class.
func AllRules() []Rule {
	var rules []Rule
	rules = append(rules, secretRules...)
	rules = append(rules, injectionRules...)
	rules = append(rules, exfiltrationRules...)
	rules = append(rules, obfuscationRules...)
	rules = append(rules, executionRules...)
	return rules
}
