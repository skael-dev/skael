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
