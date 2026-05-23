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
}
