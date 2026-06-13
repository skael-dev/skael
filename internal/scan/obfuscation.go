package scan

import "regexp"

// obfuscationRules detects attempts to hide malicious content via encoding.
var obfuscationRules = []Rule{
	{
		Name:       "OBFUSCATION",
		Category:   "obfuscation",
		Severity:   "high",
		Confidence: "high",
		// base64 decode piped to shell: "base64 -d | sh" or "base64 --decode | bash"
		Pattern: regexp.MustCompile(`(?i)base64\s+(--?decode|-d)\s*\|\s*(ba)?sh\b`),
		Message: "Obfuscation: base64-decoded content piped to shell",
	},
	{
		Name:       "OBFUSCATION",
		Category:   "obfuscation",
		Severity:   "medium",
		Confidence: "medium",
		// base64 decode command without pipe — suspicious standalone decode operation
		Pattern: regexp.MustCompile(`(?i)\bbase64\s+(--?decode|-d)\b`),
		Message: "Obfuscation: base64 decode command detected",
	},
	{
		Name:       "OBFUSCATION",
		Category:   "obfuscation",
		Severity:   "medium",
		Confidence: "low",
		// Long base64 string (50+ chars of base64 alphabet) not in a URL or common file context.
		// Must be quoted or standalone value, avoiding false positives on package hashes.
		Pattern: regexp.MustCompile(`(?:^|[\s"'=:,\[{(])[A-Za-z0-9+/]{50,}={0,2}(?:$|[\s"',\]});\n])`),
		Message: "Obfuscation: long base64-encoded string detected",
	},
	{
		Name:       "OBFUSCATION",
		Category:   "obfuscation",
		Severity:   "medium",
		Confidence: "medium",
		// Hex-encoded payload: \x followed by two hex digits, repeated 8+ times
		Pattern: regexp.MustCompile(`(\\x[0-9a-fA-F]{2}){8,}`),
		Message: "Obfuscation: hex-encoded payload detected",
	},
	{
		Name:       "OBFUSCATION",
		Category:   "obfuscation",
		Severity:   "high",
		Confidence: "high",
		// Bidirectional control characters (Trojan-Source style): these reorder how
		// text renders vs. how it is parsed and never legitimately appear in skills.
		Pattern: regexp.MustCompile(`[\x{202A}-\x{202E}\x{2066}-\x{2069}]`),
		Message: "Obfuscation: bidirectional control characters (hidden/reordered text)",
	},
	{
		Name:       "OBFUSCATION",
		Category:   "obfuscation",
		Severity:   "medium",
		Confidence: "medium",
		// Zero-width / invisible formatting characters used to hide or split text.
		// A leading BOM is stripped before scanning, so this only fires in-content.
		Pattern: regexp.MustCompile(`[\x{200B}\x{200C}\x{200D}\x{2060}\x{FEFF}\x{00AD}]`),
		Message: "Obfuscation: zero-width or invisible characters in content",
	},
}
