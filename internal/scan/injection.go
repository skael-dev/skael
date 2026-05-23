package scan

import "regexp"

// injectionRules detects prompt injection patterns in skill content.
var injectionRules = []Rule{
	{
		Name:       "PROMPT_INJECTION",
		Category:   "injection",
		Severity:   "high",
		Confidence: "high",
		// "ignore previous instructions" and variants
		Pattern: regexp.MustCompile(`(?i)\bignore\s+(all\s+)?previous\s+instructions?\b`),
		Message: "Prompt injection: attempts to override previous instructions",
	},
	{
		Name:       "PROMPT_INJECTION",
		Category:   "injection",
		Severity:   "high",
		Confidence: "high",
		// "developer mode" activation phrase
		Pattern: regexp.MustCompile(`(?i)\benable\s+developer\s+mode\b|\bdeveloper\s+mode\s+(enabled|activated|on)\b`),
		Message: "Prompt injection: attempts to activate developer mode",
	},
	{
		Name:       "PROMPT_INJECTION",
		Category:   "injection",
		Severity:   "high",
		Confidence: "high",
		// "override safety" / "bypass safety" / "disable safety filters"
		Pattern: regexp.MustCompile(`(?i)\b(override|bypass|disable|circumvent)\s+(safety|content|moderation|filter)s?\b`),
		Message: "Prompt injection: attempts to override safety filters",
	},
	{
		Name:       "PROMPT_INJECTION",
		Category:   "injection",
		Severity:   "high",
		Confidence: "high",
		// "disregard (all) (your) rules/guidelines/constraints"
		Pattern: regexp.MustCompile(`(?i)\bdisregard\s+(all\s+)?(your\s+)?(rules?|guidelines?|constraints?|instructions?)\b`),
		Message: "Prompt injection: attempts to disregard model rules",
	},
	{
		Name:       "PROMPT_INJECTION",
		Category:   "injection",
		Severity:   "medium",
		Confidence: "medium",
		// "you are now [DAN/jailbreak persona]" pattern
		Pattern: regexp.MustCompile(`(?i)\byou\s+are\s+now\s+(dan|jailbroken|unrestricted|uncensored|an?\s+ai\s+without\s+(restrictions?|limitations?))\b`),
		Message: "Prompt injection: attempts to assign jailbreak persona",
	},
}
