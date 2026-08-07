package lint_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/spec"
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

// filler is one line of the padding text used to build oversized bodies
// below. It is 26 bytes including the trailing newline, so the token math in
// the comments on each fixture is exact (bytes/4, integer division).
const filler = "Filler line of body text.\n"

func TestQuality_BodyLengthBudgets(t *testing.T) {
	// 900 lines of filler -> a 23,401-byte body (leading blank line from the
	// frontmatter's closing delimiter + 900*26 bytes) -> ApproxTokens = 5850,
	// comfortably over both the 500-line and 5000-token budgets.
	long := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		strings.Repeat(filler, 900)
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": long})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "body-too-long") {
		t.Errorf("no body-too-long finding: %v", rules(got))
	}
	if !hasRule(got, "body-token-budget") {
		t.Errorf("no body-token-budget finding: %v", rules(got))
	}
}

// TestQuality_LineBudgetIsIndependentOfTokenBudget proves the two body
// budgets fire independently: this fixture is long enough (601 lines) to trip
// the 500-line cap but its 15,601-byte body is only ~3900 approx tokens,
// under the 5000-token cap. If either rule's threshold were miscalibrated to
// piggyback on the other (as happened before this fix — the token budget had
// been lowered to make a single fixture trip both rules, which silently
// dropped legitimate 12-20KB bodies into the error path) this test would
// catch it: it would see body-token-budget fire when it must not.
func TestQuality_LineBudgetIsIndependentOfTokenBudget(t *testing.T) {
	long := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		strings.Repeat(filler, 600)
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": long})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "body-too-long") {
		t.Errorf("no body-too-long finding: %v", rules(got))
	}
	if hasRule(got, "body-token-budget") {
		t.Errorf("body-token-budget fired on a body under the token budget: %v", rules(got))
	}
}

// TestQuality_MetadataTokenBudgetFires verifies the metadata budget is
// actually reachable, rather than assuming a 100-approx-token threshold is
// correct just because it matches the design doc: a real frontmatter
// description padded past 400 bytes (100 tokens * 4 bytes/token) must trip
// it, and a normal short description must not.
func TestQuality_MetadataTokenBudgetFires(t *testing.T) {
	longDesc := "Use when a PDF is mentioned. " + strings.Repeat("Extra trigger detail. ", 30)
	body := "---\nname: pdf-extract\ndescription: " + longDesc + "\n---\n\n" +
		"1. Run scripts/extract.py. Postcondition: out/ exists.\n"
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "metadata-token-budget") {
		t.Errorf("oversized frontmatter not flagged: %v", rules(got))
	}
	for _, f := range got {
		if f.Rule == "metadata-token-budget" && f.Severity != lint.SeverityWarn {
			t.Errorf("metadata-token-budget severity = %q, want warn", f.Severity)
		}
	}
}

func TestQuality_ShortMetadataIsUnderTokenBudget(t *testing.T) {
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": goodSkill})

	got, _ := lint.Quality(dir)
	if hasRule(got, "metadata-token-budget") {
		t.Errorf("short frontmatter flagged as over the metadata token budget: %v", rules(got))
	}
}

func TestQuality_ModuleCapIsEnforced(t *testing.T) {
	// 3 scripts + 1 asset = 4 capacity modules, over MaxModules (3). The single
	// reference must not count toward this cap.
	dir := writeBundle(t, "pdf-extract", map[string]string{
		"SKILL.md":        goodSkill,
		"scripts/a.py":    "x",
		"scripts/b.py":    "x",
		"scripts/c.py":    "x",
		"references/d.md": "x",
		"assets/e.tmpl":   "x",
	})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "too-many-modules") {
		t.Errorf("4 capacity modules not flagged: %v", rules(got))
	}
	if hasRule(got, "too-many-references") {
		t.Errorf("a single reference file was flagged as too many: %v", rules(got))
	}
}

// TestQuality_ReferenceCapIsIndependentOfModuleCap proves references/ has its
// own cap and does not count against MaxModules — a reference split out of an
// over-long body adds no capability, only lower context cost.
func TestQuality_ReferenceCapIsIndependentOfModuleCap(t *testing.T) {
	files := map[string]string{"SKILL.md": goodSkill, "scripts/a.py": "x"}
	// One past the cap, derived rather than hardcoded: this test's whole point
	// is the cap, so raising the constant must not quietly stop exercising it.
	for i := 0; i <= spec.MaxReferences; i++ {
		files[fmt.Sprintf("references/r%d.md", i)] = "x"
	}
	dir := writeBundle(t, "pdf-extract", files)

	got, _ := lint.Quality(dir)
	if !hasRule(got, "too-many-references") {
		t.Errorf("%d references not flagged: %v", spec.MaxReferences+1, rules(got))
	}
	if hasRule(got, "too-many-modules") {
		t.Errorf("one script plus references was flagged as too many capacity modules: %v", rules(got))
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

// realRulesAndConstraints is the "## Rules and constraints" numbered list
// from a real generated bundle (~/ws/.whetstone/skills/sdd-to-epics-issues,
// via `whetstone gen`), quoted verbatim. It is what exposed stepLine's
// original bug: a declarative rule list is not a sequence of executable
// steps, but every "N. " line in it was scored as one anyway, flagging 9 of
// the 10 rules as step-without-postcondition. A synthetic minimal fixture
// would not have caught this — the false positive only shows up against a
// heading and a list shape a real generation pass actually produces.
const realRulesAndConstraints = "## Rules and constraints\n\n" +
	"1. Every epic and issue cites >= 1 section id in `out/sections.json`; every issue has >= 1 verbatim `evidence` string inside a cited section's line span. Ungrounded work goes to `out/open_questions.json`. *(critical, s5/s6)*\n" +
	"2. No requirement, interface, data model, NFR target, or acceptance threshold appears in an issue without supporting `evidence`; unsupported ones become open questions with a non-empty `assumption_if_unblocked`. *(critical, s6)*\n" +
	"3. No tracker API or CLI call that creates, updates, or closes work items. Files only. *(critical, s11)*\n" +
	"4. No writes, moves, or deletes outside `out/`; the source document is byte-identical at s12 to its s1 record. *(critical, s1/s12)*\n" +
	"5. Every issue is independently assignable: self-contained `context`, >= 1 Given/When/Then criterion in order, `estimate_days` <= `config.max_issue_days`. *(major, s6)*\n" +
	"6. The `blocked_by` graph is acyclic and every edge names an issue id in `out/issues.json`. *(major, s6)*\n" +
	"7. No `out/BACKLOG.md` or `out/export.*.json` before `validate_backlog.py` exits 0 in both default and `--traceability` modes. *(major, s10/s11)*\n" +
	"8. No issue whose only cited sections are non-goal or deferred-phase sections. *(major, s6)*\n" +
	"9. Every issue title starts with an imperative verb, is <= 90 characters, and names exactly one deliverable. *(minor, s6)*\n" +
	"10. No hand-editing of `out/*.json` to make validation pass; regenerate from the producing step and let `resources/schemas/backlog.schema.json` validate it. *(major, s7)*\n"

// realTerminalFallback is the same bundle's closing paragraph, quoted
// verbatim. It states the stop-and-report concept clearly but matches none of
// terminalFallback's original four fixed phrasings, which is why a correctly
// written fallback was flagged as missing one.
const realTerminalFallback = "\n**Terminal fallback:** If a checkpoint cannot be satisfied after one retry, stop. " +
	"Do not advance to later steps, do not render `out/BACKLOG.md`, and do not hand-edit an artifact to make the gate pass. " +
	"Report to the user: which step and checkpoint failed, the failing command and its exit code, the error entries verbatim from the relevant report, and which artifacts under `out/` currently exist.\n"

func TestQuality_RealRulesListDoesNotFlagStepWithoutPostcondition(t *testing.T) {
	body := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		"# Overview\n\nSome prose.\n\n" + realRulesAndConstraints
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

	got, _ := lint.Quality(dir)
	if hasRule(got, "step-without-postcondition") {
		t.Errorf("declarative rules list was scored as steps: %v", rules(got))
	}
}

// TestQuality_ANumberedProcedureStillFlagsStepWithoutPostcondition proves the
// heading-awareness fix in checkSteps doesn't blanket-disable the rule: a
// numbered procedure under a non-declarative heading must still be checked.
func TestQuality_ANumberedProcedureStillFlagsStepWithoutPostcondition(t *testing.T) {
	body := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		"## Workflow\n\n1. Run scripts/extract.py.\n"
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "step-without-postcondition") {
		t.Errorf("a real numbered procedure was not flagged: %v", rules(got))
	}
}

func TestQuality_RealTerminalFallbackParagraphSatisfiesTheRule(t *testing.T) {
	body := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		"1. Run scripts/extract.py. Postcondition: out/ exists.\n" + realTerminalFallback
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": body})

	got, _ := lint.Quality(dir)
	if hasRule(got, "no-terminal-fallback") {
		t.Errorf("a real terminal-fallback paragraph was flagged as missing one: %v", rules(got))
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
	// that rather than implying an exact count. Uses the same over-budget
	// fixture as TestQuality_BodyLengthBudgets so this test actually exercises
	// a real body-token-budget finding rather than vacuously passing over an
	// empty finding list.
	long := "---\nname: pdf-extract\ndescription: Use when a PDF is mentioned.\n---\n\n" +
		strings.Repeat(filler, 900)
	dir := writeBundle(t, "pdf-extract", map[string]string{"SKILL.md": long})

	got, _ := lint.Quality(dir)
	if !hasRule(got, "body-token-budget") {
		t.Fatalf("no body-token-budget finding to check the message of: %v", rules(got))
	}
	for _, f := range got {
		if f.Rule == "body-token-budget" && !strings.Contains(strings.ToLower(f.Message), "approx") {
			t.Errorf("token finding does not say it is approximate: %q", f.Message)
		}
	}
}
