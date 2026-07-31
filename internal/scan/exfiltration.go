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
		Name:       "SENSITIVE_FILE_ACCESS",
		Category:   "exfiltration",
		Severity:   "medium",
		Confidence: "medium",
		// User config directory — often holds gcloud/gh/cloud credentials. Kept at
		// medium so it surfaces for review without blocking publish.
		Pattern: regexp.MustCompile(`~/\.config/|\$HOME/\.config/`),
		Message: "Attempts to access ~/.config (may contain credentials)",
	},
	{
		Name:       "DATA_EXFILTRATION",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// curl|bash or wget|bash — remote code execution pattern
		Pattern: regexp.MustCompile(`(?i)(curl|wget)\s+[^\s]+\s*\|\s*(ba)?sh\b`),
		Message: "Dangerous shell: pipe remote content to shell (RCE pattern)",
		// An RCE cradle: code arriving, not data leaving. The scanner is
		// guessing from shape, which a network-off sandbox run can overturn.
		Class: ClassExecution,
	},
	{
		Name:       "DANGEROUS_SHELL",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// /dev/tcp reverse shell pattern. Host class includes ':' and '[]' so IPv6
		// targets (e.g. /dev/tcp/::1/4444) are caught, not just IPv4/hostnames.
		Pattern: regexp.MustCompile(`/dev/tcp/[a-zA-Z0-9.:\[\]\-]+/[0-9]+`),
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
		// An RCE cradle: code arriving, not data leaving. The scanner is
		// guessing from shape, which a network-off sandbox run can overturn.
		Class: ClassExecution,
	},
	{
		Name:       "DATA_EXFILTRATION",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// PowerShell download-and-execute cradle: IEX (New-Object ...).DownloadString
		Pattern: regexp.MustCompile(`(?i)(iex|invoke-expression).{0,40}downloadstring`),
		Message: "PowerShell download-and-execute cradle (RCE pattern)",
		// An RCE cradle: code arriving, not data leaving. The scanner is
		// guessing from shape, which a network-off sandbox run can overturn.
		Class: ClassExecution,
	},
	{
		Name:       "DATA_EXFILTRATION",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// Anything piped into Invoke-Expression (e.g. iwr ... | iex).
		Pattern: regexp.MustCompile(`(?i)\|\s*iex\b`),
		Message: "Remote content piped to Invoke-Expression (RCE pattern)",
		// An RCE cradle: code arriving, not data leaving. The scanner is
		// guessing from shape, which a network-off sandbox run can overturn.
		Class: ClassExecution,
	},
	{
		Name:       "DATA_EXFILTRATION",
		Category:   "exfiltration",
		Severity:   "critical",
		Confidence: "high",
		// Download a remote file then execute it: curl/wget URL -o FILE && <run>,
		// where <run> is ./file, a shell, an interpreter, or chmod +x.
		Pattern: regexp.MustCompile(`(?i)(curl|wget)\s+.*https?://.*\s-(o|O|-output)\s+\S+\s*(&&|;)\s*(\./|sh\b|bash\b|zsh\b|source\b|chmod|python[0-9.]*\b|node\b|ruby\b|perl\b|php\b)`),
		Message: "Download remote file then execute it (RCE pattern)",
	},
}
