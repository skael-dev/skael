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
	// Class overrides the class derived from Category. Set it only where a rule
	// does not share its category's appealability — an RCE cradle sits in
	// exfiltration.go but is a guess a sandbox run can overturn, while a
	// reverse shell in the same file is the exfiltration channel itself.
	//
	// Every override carries a comment saying why that rule's appealability
	// differs from its category's. An override without a stated reason is a
	// review defect: nothing in the code verifies that an override is
	// correct, only that it names a class the gate recognises, so the
	// justification in the comment is the only check there is.
	Class Class
}

// ResolvedClass is the class a finding from this rule carries: the explicit
// override when the rule sets one, otherwise the class its category implies.
// The bool is false only when neither is available, which
// TestEveryRuleHasAClass keeps unreachable.
func (r Rule) ResolvedClass() (Class, bool) {
	if r.Class != "" {
		return r.Class, true
	}
	return ClassOf(r.Category)
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
