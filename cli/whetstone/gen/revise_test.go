package gen_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// goodBody and goodDescription are the same content the default scripted body and
// description entries use — already proven clean by
// TestGenerate_OutputPassesItsOwnLint — reused here as the "revision that
// fixes it" response.
const goodBody = "# PDF Extract\n\n1. Run `scripts/extract.py <input.pdf>`. Postcondition: out/tables.csv exists.\n" +
	"2. Run `scripts/validate.py out/tables.csv`. Postcondition: exits 0.\n\n" +
	"If a checkpoint cannot be satisfied after one retry, stop and report state.\n"

// oversizedBody is a single long line (no numbered steps, no newlines beyond
// the heading) so it trips only body-token-budget — not body-too-long,
// hedge-word, or the step-level rules — keeping the fixture's one finding
// unambiguous.
var oversizedBody = "# Oversized\n\n" + strings.Repeat("word ", 5000)

// bodyJSON and descJSON marshal a gen response so the fixture text needs no
// manual JSON escaping.
func bodyJSON(t *testing.T, body string) string {
	t.Helper()
	b, err := json.Marshal(struct {
		Body string `json:"body"`
	}{body})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func descJSON(t *testing.T, desc string) string {
	t.Helper()
	b, err := json.Marshal(struct {
		Description string `json:"description"`
	}{desc})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestGenerate_RevisesAnOverBudgetBodyUntilItLints scripts a first body
// response that blows the token budget and a revision response that doesn't,
// and asserts Generate calls gen.revise.body and the written bundle lints
// clean.
func TestGenerate_RevisesAnOverBudgetBodyUntilItLints(t *testing.T) {
	s := genSpec()
	g := genFake(t, genRoles{
		Body:        []string{bodyJSON(t, oversizedBody), bodyJSON(t, goodBody)},
		Description: []string{descJSON(t, "Extracts tables from PDF files into CSV. Use when the user mentions a PDF.")},
	})

	b, err := gen.Generate(context.Background(), g, s, t.TempDir())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var sawRevision bool
	for _, c := range g.Calls() {
		if c.Role == "gen.revise.body" {
			sawRevision = true
		}
	}
	if !sawRevision {
		t.Error("Generate did not call gen.revise.body for an over-budget body")
	}

	res, err := lint.Run(b.Dir)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if res.HasErrors() {
		t.Errorf("bundle still has lint errors after revision: %+v", res.Findings)
	}
}

// oversizedBodyWithDeclarativeSection is oversizedBody's over-budget filler,
// but moved under a declarative "## Rules and constraints" heading rather
// than sitting directly under the title — offload can move a declarative
// section, so this fixture is what proves it runs, rather than always
// falling through to the model revision oversizedBody alone exercises.
var oversizedBodyWithDeclarativeSection = "# PDF Extract\n\n" +
	"1. Run `scripts/extract.py <input.pdf>`. Postcondition: out/tables.csv exists.\n\n" +
	"If a checkpoint cannot be satisfied after one retry, stop and report state.\n\n" +
	"## Rules and constraints\n\n" + strings.Repeat("word ", 5000) + "\n"

// TestGenerate_OffloadsADeclarativeSectionBeforeCallingTheModel scripts a
// body over budget entirely because of one oversized declarative section.
// Offload alone should clear the budget, so Generate must not spend a
// gen.revise.body call — the deterministic pass runs first and, here, is
// sufficient on its own.
func TestGenerate_OffloadsADeclarativeSectionBeforeCallingTheModel(t *testing.T) {
	s := genSpec()
	g := genFake(t, genRoles{
		Body:        []string{bodyJSON(t, oversizedBodyWithDeclarativeSection)},
		Description: []string{descJSON(t, "Extracts tables from PDF files into CSV. Use when the user mentions a PDF.")},
	})

	b, err := gen.Generate(context.Background(), g, s, t.TempDir())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, c := range g.Calls() {
		if c.Role == "gen.revise.body" {
			t.Error("Generate called gen.revise.body even though offload alone clears the budget")
		}
	}

	var foundRef bool
	for _, f := range b.Files {
		if f == "references/rules-and-constraints.md" {
			foundRef = true
		}
	}
	if !foundRef {
		t.Errorf("offloaded reference file is not in Bundle.Files: %v", b.Files)
	}

	res, err := lint.Run(b.Dir)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if res.HasErrors() {
		t.Errorf("bundle has lint errors after offload: %+v", res.Findings)
	}
}

// TestGenerate_RevisionCapsAtTwoAttempts scripts a body that never clears
// budget, including across both revision calls, and asserts Generate makes
// exactly two gen.revise.body calls, then returns the bundle with a nil
// error — the CLI's own lint gate is what decides failure, not this loop.
func TestGenerate_RevisionCapsAtTwoAttempts(t *testing.T) {
	s := genSpec()
	g := genFake(t, genRoles{
		Body: []string{
			bodyJSON(t, oversizedBody), bodyJSON(t, oversizedBody), bodyJSON(t, oversizedBody),
		},
		Description: []string{descJSON(t, "Extracts tables from PDF files into CSV. Use when the user mentions a PDF.")},
	})

	b, err := gen.Generate(context.Background(), g, s, t.TempDir())
	if err != nil {
		t.Fatalf("Generate returned an error for a revision that never clears: %v", err)
	}
	if b == nil {
		t.Fatal("Generate returned a nil bundle")
	}

	var revisions int
	for _, c := range g.Calls() {
		if c.Role == "gen.revise.body" {
			revisions++
		}
	}
	if revisions != 2 {
		t.Errorf("made %d gen.revise.body calls, want exactly 2", revisions)
	}
}

// TestGenerate_UnfixableFindingMakesNoRevisionCall plans four scripts against
// spec.MaxModules (3, scripts+assets only — see spec.MaxModules), which trips
// too-many-modules — a finding neither the body nor description pass can fix
// by rewriting text. Generate must not spend a call trying.
func TestGenerate_UnfixableFindingMakesNoRevisionCall(t *testing.T) {
	s := genSpec()
	s.Resources = spec.ResourcePlan{
		Scripts: []spec.ResourceItem{
			{Path: "scripts/extract.py"},
			{Path: "scripts/validate.py"},
			{Path: "scripts/normalize.py"},
			{Path: "scripts/report.py"},
		},
	}

	g := genFake(t, genRoles{
		Body:        []string{bodyJSON(t, goodBody)},
		Description: []string{descJSON(t, "Extracts tables from PDF files into CSV. Use when the user mentions a PDF.")},
		Resource:    func(string) string { return `{"content":"print(1)"}` },
	})

	b, err := gen.Generate(context.Background(), g, s, t.TempDir())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	res, err := lint.Run(b.Dir)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if !hasFinding(res, "too-many-modules") {
		t.Fatalf("fixture did not actually trip too-many-modules, findings: %+v", res.Findings)
	}

	wantCalls := 7 // outline, body, 4 resources, description
	if len(g.Calls()) != wantCalls {
		t.Errorf("made %d gateway calls, want %d (no revision call for an unfixable finding)", len(g.Calls()), wantCalls)
	}
	for _, c := range g.Calls() {
		if strings.HasPrefix(c.Role, "gen.revise") {
			t.Errorf("unexpected revision call with role %q", c.Role)
		}
	}
}

func hasFinding(res *lint.Result, rule string) bool {
	for _, f := range res.Findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}
