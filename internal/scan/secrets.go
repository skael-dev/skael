package scan

import "regexp"

// secretRules detects hardcoded credentials and API keys.
var secretRules = []Rule{
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// OpenAI project key: sk-proj- followed by at least 20 alphanumeric chars
		Pattern: regexp.MustCompile(`sk-proj-[A-Za-z0-9_\-]{20,}`),
		Message: "OpenAI project API key detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// Anthropic key: sk-ant- followed by at least 20 alphanumeric chars
		Pattern: regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`),
		Message: "Anthropic API key detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// AWS access key ID: AKIA followed by 16 uppercase alphanumeric chars
		Pattern: regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
		Message: "AWS access key ID detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// GitHub personal access token: ghp_ followed by at least 36 alphanumeric chars
		Pattern: regexp.MustCompile(`ghp_[A-Za-z0-9]{36,}`),
		Message: "GitHub personal access token detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// Stripe live secret key: sk_live_ followed by at least 20 alphanumeric chars
		Pattern: regexp.MustCompile(`sk_live_[A-Za-z0-9]{20,}`),
		Message: "Stripe live secret key detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "high",
		Confidence: "medium",
		// Bearer token in Authorization header value (at least 20 chars, not a placeholder)
		Pattern: regexp.MustCompile(`(?i)Authorization:\s*Bearer\s+[A-Za-z0-9\-._~+/]{20,}={0,2}`),
		Message: "Hardcoded Bearer token in Authorization header",
	},
}
