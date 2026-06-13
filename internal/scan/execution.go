package scan

import "regexp"

// executionRules detects dynamic code-execution patterns (PRD §Shell dangers
// lists "eval"). These match the dangerous construct shape — eval/exec with an
// argument, dynamic import, shell -c on a variable — not the bare words, so
// prose like "execute the test suite" and Go's exec.Command(...) do not trip.
var executionRules = []Rule{
	{
		Name:       "CODE_EXECUTION",
		Category:   "execution",
		Severity:   "high",
		Confidence: "high",
		// eval followed by an opening paren, quote, or backtick: eval(...), eval "$x".
		Pattern: regexp.MustCompile(`(?i)\beval\s*[("'` + "`" + `]`),
		Message: "Dynamic code execution via eval",
	},
	{
		Name:       "CODE_EXECUTION",
		Category:   "execution",
		Severity:   "high",
		Confidence: "medium",
		// Python/JS exec( with an argument. exec.Command (Go) has a dot, not a paren.
		Pattern: regexp.MustCompile(`\bexec\s*\(`),
		Message: "Dynamic code execution via exec()",
	},
	{
		Name:       "CODE_EXECUTION",
		Category:   "execution",
		Severity:   "high",
		Confidence: "high",
		// Python dynamic import, often used to obscure os/subprocess calls.
		Pattern: regexp.MustCompile(`__import__\s*\(`),
		Message: "Dynamic module import via __import__()",
	},
	{
		Name:       "CODE_EXECUTION",
		Category:   "execution",
		Severity:   "high",
		Confidence: "medium",
		// A shell invoked with -c on a variable/interpolation (executes dynamic
		// content). Static command strings (bash -c "ls -la") are not matched.
		Pattern: regexp.MustCompile(`(?i)\b(sh|bash|zsh|ksh|dash)\s+-c\s+["']?\$`),
		Message: "Shell executes a dynamic command string",
	},
}
