package scan

import "regexp"

// exfiltrationRules detects data exfiltration attempts and dangerous shell patterns.
var exfiltrationRules = []Rule{
	{
		Name:       "SENSITIVE_FILE_ACCESS",
		Category:   "exfiltration",
		Severity:   "high",
		Confidence: "high",
		// Direct .env file read instructions
		Pattern: regexp.MustCompile(`(?i)\bread\s+(the\s+)?\.env\s+(file\b|and\b)|cat\s+\.env\b|open\s+\.env\b`),
		Message: "Attempts to read .env credentials file",
	},
	{
		Name:       "SENSITIVE_FILE_ACCESS",
		Category:   "exfiltration",
		Severity:   "high",
		Confidence: "high",
		// SSH key directory access
		Pattern: regexp.MustCompile(`~/\.ssh/|~\/\.ssh\b|\$HOME/\.ssh/`),
		Message: "Attempts to access SSH key directory",
	},
	{
		Name:       "SENSITIVE_FILE_ACCESS",
		Category:   "exfiltration",
		Severity:   "high",
		Confidence: "high",
		// AWS credentials file access
		Pattern: regexp.MustCompile(`~/\.aws/credentials|~\/\.aws\b|\$HOME/\.aws/`),
		Message: "Attempts to access AWS credentials",
	},
	{
		Name:       "DATA_EXFILTRATION",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// curl|bash or wget|bash — remote code execution pattern
		Pattern: regexp.MustCompile(`(?i)(curl|wget)\s+[^\s]+\s*\|\s*(ba)?sh\b`),
		Message: "Dangerous shell: pipe remote content to shell (RCE pattern)",
	},
	{
		Name:       "DANGEROUS_SHELL",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// /dev/tcp reverse shell pattern
		Pattern: regexp.MustCompile(`/dev/tcp/[a-zA-Z0-9\.\-]+/[0-9]+`),
		Message: "Dangerous shell: /dev/tcp reverse shell pattern detected",
	},
	{
		Name:       "DATA_EXFILTRATION",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// Exfiltration of well-known secret env vars via curl/wget/nc
		Pattern: regexp.MustCompile(`(?i)(curl|wget|nc|ncat)\s+.*\$(\{)?(ANTHROPIC_API_KEY|OPENAI_API_KEY|AWS_SECRET_ACCESS_KEY|AWS_ACCESS_KEY_ID|DATABASE_URL|SECRET_KEY|PRIVATE_KEY)(\})?`),
		Message: "Attempts to exfiltrate secret environment variable",
	},
	{
		Name:       "DATA_EXFILTRATION",
		Category:   "exfiltration",
		Severity:   "high",
		Confidence: "medium",
		// "fetch and execute" remote script instructions
		Pattern: regexp.MustCompile(`(?i)\bfetch\s+and\s+(execute|run)\b`),
		Message: "Instruction to fetch and execute remote code",
	},
}
