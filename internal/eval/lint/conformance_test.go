package lint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/lint"
)

// writeBundle creates a skill bundle on disk and returns its directory.
func writeBundle(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const goodSkill = `---
name: pdf-extract
description: Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction.
license: Apache-2.0
---

# PDF Extract

1. Run ` + "`scripts/extract.py <input.pdf>`" + `. Postcondition: out/tables.csv exists.
2. Run ` + "`scripts/validate.py out/tables.csv`" + `. Postcondition: exits 0.

If a checkpoint cannot be satisfied after one retry, stop and report state.
`

func rules(fs []lint.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}

func hasRule(fs []lint.Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestConformance_CleanBundleHasNoErrors(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md":            goodSkill,
		"scripts/extract.py":  "#!/usr/bin/env python3\n",
		"scripts/validate.py": "#!/usr/bin/env python3\n",
	})

	got, err := lint.Conformance(dir)
	if err != nil {
		t.Fatalf("Conformance: %v", err)
	}
	for _, f := range got {
		if f.Severity == lint.SeverityError {
			t.Errorf("clean bundle produced an error finding: %+v", f)
		}
	}
}

func TestConformance_MissingSkillMDIsAnError(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{"README.md": "hi"})

	got, err := lint.Conformance(dir)
	if err != nil {
		t.Fatalf("Conformance: %v", err)
	}
	if !hasRule(got, "missing-skill-md") {
		t.Errorf("no missing-skill-md finding: %v", rules(got))
	}
}

func TestConformance_NameMustMatchDirectory(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md": strings.Replace(goodSkill, "name: pdf-extract", "name: something-else", 1),
	})

	got, _ := lint.Conformance(dir)
	if !hasRule(got, "name-dir-mismatch") {
		t.Errorf("no name-dir-mismatch finding: %v", rules(got))
	}
}

func TestConformance_DescriptionLimits(t *testing.T) {
	t.Run("over 1024 bytes is an error", func(t *testing.T) {
		dir := writeBundle(t, "pdf-extract", map[string]string{
			"SKILL.md": strings.Replace(goodSkill,
				"Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction.",
				strings.Repeat("x", 1025), 1),
		})
		got, _ := lint.Conformance(dir)
		if !hasRule(got, "description-too-long") {
			t.Errorf("no description-too-long finding: %v", rules(got))
		}
	})

	t.Run("missing is an error", func(t *testing.T) {
		dir := writeBundle(t, "pdf-extract", map[string]string{
			"SKILL.md": "---\nname: pdf-extract\n---\n\n# x\n",
		})
		got, _ := lint.Conformance(dir)
		if !hasRule(got, "description-missing") {
			t.Errorf("no description-missing finding: %v", rules(got))
		}
	})
}

func TestConformance_NonSpecNameIsAnError(t *testing.T) {
	// Colons are legal in a registry name but not in a spec name or a directory.
	dir := writeBundle(t, "brainstorming", map[string]string{
		"SKILL.md": strings.Replace(goodSkill, "name: pdf-extract", "name: superpowers:brainstorming", 1),
	})
	got, _ := lint.Conformance(dir)
	if !hasRule(got, "name-not-spec-compliant") {
		t.Errorf("no name-not-spec-compliant finding: %v", rules(got))
	}
}

func TestConformance_BrokenRelativeLinkIsAnError(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md": goodSkill + "\nSee [the reference](references/missing.md).\n",
	})
	got, _ := lint.Conformance(dir)
	if !hasRule(got, "broken-link") {
		t.Errorf("no broken-link finding: %v", rules(got))
	}
}

func TestConformance_ExternalLinksAreNotCheckedForExistence(t *testing.T) {
	// Linting must not make network calls; an http link is not a broken link.
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md":            goodSkill + "\nSee [docs](https://example.com/x).\n",
		"scripts/extract.py":  "x",
		"scripts/validate.py": "x",
	})
	got, _ := lint.Conformance(dir)
	if hasRule(got, "broken-link") {
		t.Errorf("external link reported as broken: %v", got)
	}
}

func TestConformance_ReferenceDepthIsCappedAtOne(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md":               goodSkill,
		"references/a/b/deep.md": "too deep",
		"scripts/extract.py":     "x",
		"scripts/validate.py":    "x",
	})
	got, _ := lint.Conformance(dir)
	if !hasRule(got, "reference-too-deep") {
		t.Errorf("no reference-too-deep finding: %v", rules(got))
	}
}

func TestConformance_InvalidUTF8IsAnError(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": goodSkill})
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte{0xff, 0xfe, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := lint.Conformance(dir)
	if !hasRule(got, "invalid-utf8") {
		t.Errorf("no invalid-utf8 finding: %v", rules(got))
	}
}

func TestConformance_DelegatesToValidateSpec(t *testing.T) {
	// The point of calling skill.ValidateSpec instead of reimplementing its
	// rules is that conformance and the registry's publish-time compliance
	// check can never drift apart. This test pins that delegation down: it
	// asserts the exact warning wording skill.ValidateSpec produces. If that
	// call were ever replaced by a local reimplementation, this test would
	// only keep passing if the replacement reproduced the wording exactly —
	// otherwise it catches the drift immediately.
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md":            strings.Replace(goodSkill, "name: pdf-extract", "name: something-else", 1),
		"scripts/extract.py":  "x",
		"scripts/validate.py": "x",
	})

	got, err := lint.Conformance(dir)
	if err != nil {
		t.Fatalf("Conformance: %v", err)
	}

	var found *lint.Finding
	for i := range got {
		if got[i].Rule == "spec-warning" {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no spec-warning finding (skill.ValidateSpec's warnings were not surfaced): %v", rules(got))
	}
	if found.Severity != lint.SeverityWarn {
		t.Errorf("spec-warning severity = %q, want %q", found.Severity, lint.SeverityWarn)
	}
	const wantSubstr = `frontmatter name "something-else" differs from skill name "pdf-extract"`
	if !strings.Contains(found.Message, wantSubstr) {
		t.Errorf("spec-warning message = %q, want it to contain skill.ValidateSpec's exact wording %q", found.Message, wantSubstr)
	}
}

func TestResult_ExitCodeIgnoresWarnings(t *testing.T) {
	warnOnly := &lint.Result{Findings: []lint.Finding{{Rule: "r", Severity: lint.SeverityWarn}}}
	if got := warnOnly.ExitCode(); got != 0 {
		t.Errorf("warnings alone exit %d, want 0 — a pre-commit hook that fails on warnings gets disabled", got)
	}

	withError := &lint.Result{Findings: []lint.Finding{
		{Rule: "r", Severity: lint.SeverityWarn},
		{Rule: "s", Severity: lint.SeverityError},
	}}
	if got := withError.ExitCode(); got != 1 {
		t.Errorf("ExitCode = %d, want 1", got)
	}
	if withError.Errors() != 1 || withError.Warnings() != 1 {
		t.Errorf("Errors=%d Warnings=%d, want 1 and 1", withError.Errors(), withError.Warnings())
	}
}
