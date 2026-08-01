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
		// Two further branches, both structural rather than lexical — they
		// match what the code *does*, not a warning word the author happened
		// to write nearby, which is the bypass vector this project rules out.
		// Reject is tested against the matched text only (see scanner.go), so
		// neither can suppress a real secret elsewhere on the same line, and
		// neither applies to the prefixed-token rules above: a real
		// sk-ant-/AKIA/xox…/ghp_ key is matched by its own rule, which has no
		// Reject at all.
		//
		//  1. An environment lookup. `getenv('ANTHROPIC_API_KEY')`,
		//     `os.environ[...]`, `process.env.X`, `ENV['X']` name a secret;
		//     they do not contain one, by construction. Flagging them punishes
		//     the recommended practice.
		//  2. A redacted documentation placeholder — a value that is a short
		//     prefix followed by a literal ellipsis (`"xoxp-..."`), or whose
		//     whole body is filler (`"xxxx"`, `"****"`). Deliberately anchored
		//     to the entire value: a real key is never wholly filler, and a
		//     looser `x{4,}` anywhere would reject genuine base64 key material.
		//     That would be a false negative, which is the one direction this
		//     rule may not fail in.
		Reject: regexp.MustCompile(`(?i)["'](your|example|sample|change|placeholder|dummy|fake|redacted|todo|none|null|xxx|abc123)` +
			`|(?-i:["'][A-Z][A-Z0-9_]{2,}["'])` +
			`|\b(getenv|os\.environ|os\.Getenv|process\.env|System\.getenv|GetEnvironmentVariable)\b|\bENV\[` +
			`|["'][A-Za-z0-9_\-]{0,16}(\.\.\.|…)["']|["'][xX]{4,}["']|["']\*{3,}["']`),
		Message: "Hardcoded password or secret in plaintext",
	},
}
