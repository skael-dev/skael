package skill

import "testing"

// A report stored with a full scanner path must match a later scan that
// names the scanner by its base name, or every external finding reads as new.
func TestRuleKeyIgnoresScannerDirectory(t *testing.T) {
	cases := map[string]string{
		"/opt/tools/gitleaks:aws-key":       "gitleaks:aws-key",
		`C:\tools\gitleaks.exe:aws-key`:     "gitleaks:aws-key",
		"gitleaks:aws-key":                  "gitleaks:aws-key",
		"/opt/semgrep:python/lang/eval-use": "semgrep:python/lang/eval-use",
		"exfiltration-curl":                 "exfiltration-curl",
	}
	for in, want := range cases {
		if got := ruleKey(in); got != want {
			t.Errorf("ruleKey(%q) = %q, want %q", in, got, want)
		}
	}
}
