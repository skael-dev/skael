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
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// PEM private key block header (RSA/EC/DSA/OpenSSH/PGP/encrypted/generic).
		Pattern: regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY-----`),
		Message: "Private key block detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// Google API key: AIza followed by 35 url-safe chars.
		Pattern: regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),
		Message: "Google API key detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// Slack token: xox[bot/user/app/refresh/legacy]- followed by token body.
		Pattern: regexp.MustCompile(`xox[baprs]-[0-9A-Za-z]{10,}-[0-9A-Za-z]{10,}`),
		Message: "Slack token detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// GitLab personal access token: glpat- followed by 20+ url-safe chars.
		Pattern: regexp.MustCompile(`glpat-[0-9A-Za-z_\-]{20,}`),
		Message: "GitLab personal access token detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "critical",
		Confidence: "high",
		// GitHub fine-grained PAT: github_pat_ followed by 22+ token chars.
		Pattern: regexp.MustCompile(`github_pat_[0-9A-Za-z_]{22,}`),
		Message: "GitHub fine-grained personal access token detected",
	},
	{
		Name:       "SECRET_EXPOSURE",
		Category:   "secrets",
		Severity:   "high",
		Confidence: "medium",
		// Hardcoded password/secret/token/key assignment with a quoted literal
		// value (6+ chars). The value char class excludes $, {, }, <, >, % so that
		// env-var interpolation and templates ("${API_KEY}", "<token>") never match.
		Pattern: regexp.MustCompile(`(?i)\b(passwd|password|pwd|secret|client[_-]?secret|access[_-]?token|auth[_-]?token|api[_-]?key|apikey|secret[_-]?key)\b\s*[:=]\s*["'][^"'$\n{}<>%]{6,}["']`),
		// Suppress placeholders (value starts with a placeholder word) and quoted
		// ALL-CAPS env-var name references like "API_KEY". The env-name branch is
		// case-sensitive (?-i:) so it does not reject real lowercase secret values.
		// The word list is deliberately limited to UNAMBIGUOUS placeholders — words
		// like "secret"/"token"/"test" are omitted because real secret values
		// commonly start with them, and rejecting those would be a false negative.
		Reject:  regexp.MustCompile(`(?i)["'](your|example|sample|change|placeholder|dummy|fake|redacted|todo|none|null|xxx|abc123)|(?-i:["'][A-Z][A-Z0-9_]{2,}["'])`),
		Message: "Hardcoded password or secret in plaintext",
	},
}
