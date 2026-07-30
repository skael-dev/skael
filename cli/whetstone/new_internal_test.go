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
const brokenBody = `{"body":"# PDF Extract\n\nSee [the format notes](references/missing.md).\n\n1. Run ` +
	"`scripts/extract.py <input.pdf>`" + `. Postcondition: out/tables.csv exists.\n\nIf a checkpoint cannot be satisfied after one retry, stop and report state.\n"}`

const (
	outlinePass     = `{"sections":["Overview","Steps","Failure handling"]}`
	resourcesPass   = `{"files":[]}`
	descriptionPass = `{"description":"Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction."}`
)

// newScript returns every scripted gateway response a full `new` run consumes,
// in order: two interview passes, four generation passes, one suite draft.
func newScript(body string) []string {
	return []string{
		specDraft, specDraft,
		outlinePass, body, resourcesPass, descriptionPass,
		suiteDraft,
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

// TestRunNew_StopsBeforeTheContractWhenTheBundleFailsLint is the brief's
// load-bearing rule: a contract compiled from a spec whose bundle does not
// lint describes a skill that does not exist, and a suite drafted against it
// measures nothing. Neither artifact may be written.
func TestRunNew_StopsBeforeTheContractWhenTheBundleFailsLint(t *testing.T) {
	st := newTestStore(t)
	g := fake.New(newScript(brokenBody)...)

	err := runNew(context.Background(), st, g, strings.NewReader(""), "extract tables from PDFs", true)
	if err == nil {
		t.Fatal("runNew succeeded on a bundle that fails lint")
	}
	if !strings.Contains(err.Error(), "does not lint clean") {
		t.Errorf("error does not name the lint failure: %v", err)
	}

	contractPath, perr := st.ContractPath("pdf-extract")
	if perr != nil {
		t.Fatal(perr)
	}
	if exists(t, contractPath) {
		t.Error("a contract was written for a bundle that fails lint")
	}

	suiteDir, perr := st.SuiteDir("pdf-extract")
	if perr != nil {
		t.Fatal(perr)
	}
	if exists(t, suiteDir) {
		t.Error("a suite was written for a bundle that fails lint")
	}

	// The suite draft must not have been requested either: it is the most
	// expensive call in the pipeline, and stopping "after the contract" would
	// still have spent it.
	if n := len(g.Calls()); n != 6 {
		t.Errorf("gateway calls = %d, want 6 (2 interview + 4 generation, no suite draft)", n)
	}
}

// TestRunNew_WritesTheSidecarWhenTheBundleLintsClean is the positive control
// for the test above: with the only difference being a bundle that lints, both
// artifacts must appear.
func TestRunNew_WritesTheSidecarWhenTheBundleLintsClean(t *testing.T) {
	st := newTestStore(t)
	g := fake.New(newScript(cleanBody)...)

	if err := runNew(context.Background(), st, g, strings.NewReader(""), "extract tables from PDFs", true); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	contractPath, err := st.ContractPath("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if !exists(t, contractPath) {
		t.Error("no contract was written for a bundle that lints clean")
	}

	suiteDir, err := st.SuiteDir("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := suite.Load(suiteDir)
	if err != nil {
		t.Fatalf("no suite was written: %v", err)
	}
	if len(loaded.Tasks) != 2 {
		t.Errorf("suite has %d tasks, want 2", len(loaded.Tasks))
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
