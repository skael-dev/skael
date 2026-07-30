package lint_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/lint"
)

func TestQuality_CleanBundleHasNoErrors(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md":            goodSkill,
		"scripts/extract.py":  "x",
		"scripts/validate.py": "x",
	})

	got, err := lint.Quality(dir)
	if err != nil {
		t.Fatalf("Quality: %v", err)
	}
	for _, f := range got {
		if f.Severity == lint.SeverityError {
			t.Errorf("clean bundle produced an error: %+v", f)
		}
	}
}

func TestQuality_HedgeWordsInStepsAreErrors(t *testing.T) {
	for _, hedge := range []string{"consider", "if appropriate", "as needed", "ideally"} {
		t.Run(hedge, func(t *testing.T) {
			body := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
				"1. " + strings.ToUpper(hedge[:1]) + hedge[1:] + " running scripts/extract.py. Postcondition: out/ exists.\n"
			dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

			got, _ := lint.Quality(dir)
			if !hasRule(got, "hedge-word") {
				t.Errorf("hedge %q not flagged: %v", hedge, rules(got))
			}
		})
	}
}

func TestQuality_HedgeWordsOutsideStepsAreNotFlagged(t *testing.T) {
	// Prose may legitimately say "consider". Only step text is binding, and
	// flagging prose would train users to ignore the lint.
	body := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		"This skill exists because manual extraction is error prone; consider the alternatives.\n\n" +
		"1. Run scripts/extract.py. Postcondition: out/tables.csv exists.\n"
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

	got, _ := lint.Quality(dir)
	if hasRule(got, "hedge-word") {
		t.Errorf("hedge word in prose was flagged: %v", got)
	}
}

func TestQuality_BodyLengthBudgets(t *testing.T) {
	long := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		strings.Repeat("Filler line of body text.\n", 600)
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": long})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "body-too-long") {
		t.Errorf("no body-too-long finding: %v", rules(got))
	}
	if !hasRule(got, "body-token-budget") {
		t.Errorf("no body-token-budget finding: %v", rules(got))
	}
}

func TestQuality_ModuleCapIsEnforced(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md":        goodSkill,
		"scripts/a.py":    "x",
		"scripts/b.py":    "x",
		"references/c.md": "x",
		"assets/d.tmpl":   "x",
	})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "too-many-modules") {
		t.Errorf("4 modules not flagged: %v", rules(got))
	}
}

func TestQuality_MissingTerminalFallbackIsAWarning(t *testing.T) {
	body := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		"1. Run scripts/extract.py. Postcondition: out/tables.csv exists.\n"
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "no-terminal-fallback") {
		t.Errorf("no no-terminal-fallback finding: %v", rules(got))
	}
	for _, f := range got {
		if f.Rule == "no-terminal-fallback" && f.Severity != lint.SeverityWarn {
			t.Errorf("no-terminal-fallback severity = %q, want warn", f.Severity)
		}
	}
}

func TestQuality_DescriptionWithoutTriggerLanguageIsAWarning(t *testing.T) {
	body := "---\nname: pdf-extract\ndescription: Extracts tables from PDFs.\n---\n\n" +
		"1. Run scripts/extract.py. Postcondition: out/ exists.\n"
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "description-no-trigger") {
		t.Errorf("description without 'use when' not flagged: %v", rules(got))
	}
}

func TestApproxTokens(t *testing.T) {
	if got := lint.ApproxTokens(strings.Repeat("a", 400)); got != 100 {
		t.Errorf("ApproxTokens = %d, want 100", got)
	}
	if got := lint.ApproxTokens(""); got != 0 {
		t.Errorf("ApproxTokens(\"\") = %d, want 0", got)
	}
}

func TestQuality_TokenFindingsSayApproximate(t *testing.T) {
	// The budget is checked against an approximation, and the message must admit
	// that rather than implying an exact count.
	long := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		strings.Repeat("Filler line of body text.\n", 600)
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": long})

	got, _ := lint.Quality(dir)
	for _, f := range got {
		if f.Rule == "body-token-budget" && !strings.Contains(strings.ToLower(f.Message), "approx") {
			t.Errorf("token finding does not say it is approximate: %q", f.Message)
		}
	}
}
