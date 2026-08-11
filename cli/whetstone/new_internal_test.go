package whetstone

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// specDraft is what the interview's two passes return. Both passes get the
// same answer: the critique pass is a repair, and there is nothing to repair.
const specDraft = `{
  "name": "pdf-extract",
  "purpose": "Extract tables from PDFs.",
  "description": "Extracts tables from PDF files into CSV. Use when the user mentions a PDF.",
  "triggers": [{"text": "extract tables from this pdf"}],
  "steps": [{"id": "s1", "action": "Run scripts/extract.py", "postcondition": "out/tables.csv exists"}],
  "target_tier": "mid"
}`

// cleanBody is the generator's body pass for a bundle that lints clean.
const cleanBody = `{"body":"# PDF Extract\n\n1. Run ` + "`scripts/extract.py <input.pdf>`" +
	`. Postcondition: out/tables.csv exists.\n\nIf a checkpoint cannot be satisfied after one retry, stop and report state.\n"}`

// brokenBody links to a file the bundle does not contain, which lint reports
// as a broken-link error. It is the cheapest way to make generation produce a
// bundle that fails lint without reaching into the generator.
//
// broken-link is body-owned, so gen's revision loop tries to fix it — scripting
// the same broken body back as each revision response is what exercises the
// case that matters here: revision exhausted, bundle still bad, gate holds.
const brokenBody = `{"body":"# PDF Extract\n\nSee [the format notes](references/missing.md).\n\n1. Run ` +
	"`scripts/extract.py <input.pdf>`" + `. Postcondition: out/tables.csv exists.\n\nIf a checkpoint cannot be satisfied after one retry, stop and report state.\n"}`

const (
	outlinePass     = `{"sections":["Overview","Steps","Failure handling"]}`
	descriptionPass = `{"description":"Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction."}`
)

// evalSetDraft is the eval set suite.Generate drafts in one call.
const evalSetDraft = `{"evals": [
  {"prompt": "extract the tables", "expected_output": "a csv",
   "expectations": ["out/tables.csv exists"]},
  {"prompt": "pull the tables out", "expected_output": "a csv",
   "expectations": ["out/tables.csv exists"]}
]}`

// newScript returns every scripted gateway response a full `new` run
// consumes, in order: two interview passes, three generation passes, and one
// eval set draft. specDraft plans no resource files, so the generator makes
// no resources-pass call at all — there is no fourth generation response to
// script.
func newScript(body string) []string {
	return []string{
		specDraft, specDraft,
		outlinePass, body, descriptionPass,
		evalSetDraft,
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// TestRunNew_StopsBeforeTheEvalSetWhenTheBundleFailsLint is the brief's
// load-bearing rule: an eval set drafted against a bundle that does not lint
// measures a skill that does not exist, so it must not be written.
func TestRunNew_StopsBeforeTheEvalSetWhenTheBundleFailsLint(t *testing.T) {
	st := newTestStore(t)
	// No suite draft is scripted: reaching it is itself the failure this test
	// is looking for, and the fake reports an unscripted call by name.
	g := fake.New(
		specDraft, specDraft,
		outlinePass, brokenBody, descriptionPass,
		brokenBody, brokenBody,
	)

	err := runNew(context.Background(), st, g, strings.NewReader(""), "extract tables from PDFs", true)
	if err == nil {
		t.Fatal("runNew succeeded on a bundle that fails lint")
	}
	if !strings.Contains(err.Error(), "does not lint clean") {
		t.Errorf("error does not name the lint failure: %v", err)
	}

	suiteDir, perr := st.SuiteDir("pdf-extract")
	if perr != nil {
		t.Fatal(perr)
	}
	if exists(t, suiteDir) {
		t.Error("an eval set was written for a bundle that fails lint")
	}

	// The eval set draft must not have been requested either: it is the most
	// expensive call left in the pipeline.
	if n := len(g.Calls()); n != 7 {
		t.Errorf("gateway calls = %d, want 7 (2 interview + 3 generation — specDraft plans no resources — "+
			"+ 2 exhausted body revisions, no suite draft)", n)
	}
}

// TestRunNew_WritesTheSidecarWhenTheBundleLintsClean is the positive control
// for the test above: with the only difference being a bundle that lints, the
// eval set must appear.
func TestRunNew_WritesTheSidecarWhenTheBundleLintsClean(t *testing.T) {
	st := newTestStore(t)
	g := fake.New(newScript(cleanBody)...)

	if err := runNew(context.Background(), st, g, strings.NewReader(""), "extract tables from PDFs", true); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	suiteDir, err := st.SuiteDir("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := suite.LoadEvalSet(suiteDir)
	if err != nil {
		t.Fatalf("no eval set was written: %v", err)
	}
	if len(loaded.Evals) == 0 {
		t.Error("the written eval set has no evals")
	}
}

// TestRunNew_ApprovalGateDeclines covers every answer that is not consent,
// including the empty one: the prompt is "[y/N]", so a bare Enter must stop
// the run rather than approve a spec nobody read.
func TestRunNew_ApprovalGateDeclines(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{"n", "n\n"},
		{"bare enter", "\n"},
		{"no", "no\n"},
		{"closed stdin", ""},
		{"maybe", "maybe\n"},
		{"not quite yes", "Y es\n"},
	}
	for _, tc := range cases {
		answer := tc.answer
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			// Only the interview is scripted: if the gate leaks, generation
			// runs out of responses and the failure names the wrong step.
			g := fake.New(specDraft, specDraft)

			err := runNew(context.Background(), st, g, strings.NewReader(answer), "extract tables from PDFs", false)
			if err == nil {
				t.Fatalf("runNew proceeded on answer %q", answer)
			}
			if !strings.Contains(err.Error(), "was not approved") {
				t.Errorf("answer %q stopped for the wrong reason: %v", answer, err)
			}

			// The spec is still stored — the draft is not thrown away — but
			// the version must not be approved, or `gen` would accept it.
			if isApproved(st, "pdf-extract", 1) {
				t.Errorf("answer %q approved the spec", answer)
			}
			if n := len(g.Calls()); n != 2 {
				t.Errorf("gateway calls = %d, want 2: generation ran despite a declined gate", n)
			}
		})
	}
}

// TestRunNew_ApprovalGateAccepts pins the other half: "y" and "yes" consent,
// case-insensitively and ignoring surrounding whitespace.
func TestRunNew_ApprovalGateAccepts(t *testing.T) {
	for _, answer := range []string{"y\n", "yes\n", "Y\n", "  yes  \n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			st := newTestStore(t)
			g := fake.New(newScript(cleanBody)...)

			if err := runNew(context.Background(), st, g, strings.NewReader(answer), "extract tables from PDFs", false); err != nil {
				t.Fatalf("answer %q was rejected: %v", answer, err)
			}
			if !isApproved(st, "pdf-extract", 1) {
				t.Errorf("answer %q did not approve the spec", answer)
			}
		})
	}
}

// TestRunNew_GenerationFailureSuggestsResume pins the resume hint from
// wrapGenerationError: completed passes are cached, so a generation failure
// should name the exact command to pick up from there rather than leaving the
// operator to already know that.
func TestRunNew_GenerationFailureSuggestsResume(t *testing.T) {
	st := newTestStore(t)
	// Only the interview is scripted, so the outline call — the first
	// generation pass — fails with no response left to serve it.
	g := fake.New(specDraft, specDraft)

	err := runNew(context.Background(), st, g, strings.NewReader(""), "extract tables from PDFs", true)
	if err == nil {
		t.Fatal("runNew succeeded with no generation responses scripted")
	}
	if !strings.Contains(err.Error(), "resume with") || !strings.Contains(err.Error(), "whetstone gen pdf-extract") {
		t.Errorf("error does not suggest the resume command: %v", err)
	}
}

// TestRunNew_SuiteFailureSuggestsResume covers the other call site sharing
// wrapGenerationError: a suite-drafting failure is resumed with `suite gen`,
// not `gen` — the bundle already generated successfully.
func TestRunNew_SuiteFailureSuggestsResume(t *testing.T) {
	st := newTestStore(t)
	// The bundle passes are all scripted; the suite draft is not.
	g := fake.New(specDraft, specDraft, outlinePass, cleanBody, descriptionPass)

	err := runNew(context.Background(), st, g, strings.NewReader(""), "extract tables from PDFs", true)
	if err == nil {
		t.Fatal("runNew succeeded with no suite-draft response scripted")
	}
	if !strings.Contains(err.Error(), "resume with") || !strings.Contains(err.Error(), "whetstone suite gen pdf-extract") {
		t.Errorf("error does not suggest the suite resume command: %v", err)
	}
}

// TestRunNew_YesSkipsThePromptEntirely gives --yes an input that would decline
// if it were read. The flag must bypass the gate, not answer it.
func TestRunNew_YesSkipsThePromptEntirely(t *testing.T) {
	st := newTestStore(t)
	g := fake.New(newScript(cleanBody)...)

	declining := strings.NewReader("n\n")
	if err := runNew(context.Background(), st, g, declining, "extract tables from PDFs", true); err != nil {
		t.Fatalf("runNew --yes: %v", err)
	}
	if !isApproved(st, "pdf-extract", 1) {
		t.Error("--yes did not approve the spec")
	}
	if declining.Len() == 0 {
		t.Error("--yes consumed the approval input; it must not read stdin at all")
	}
}
