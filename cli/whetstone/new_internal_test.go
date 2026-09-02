package whetstone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"

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

// newFake answers a `new` run by role. The bundle generation and the eval set
// draft now run beside each other, and the generator's own passes run beside
// each other too, so there is no call order to script against.
//
// body is a queue: the first entry answers gen.body, each later one answers a
// revision call. evalSet of "" makes the eval set draft fail, which is how a
// test asserts the eval-set half of the run without scripting a success.
func newFake(t *testing.T, body []string, evalSet string) *fake.Gateway {
	t.Helper()
	var mu sync.Mutex
	return fake.NewFunc(func(r llm.Req) (string, error) {
		switch r.Role {
		case "interview.draft", "interview.critique":
			return specDraft, nil
		case "gen.outline":
			return outlinePass, nil
		case "gen.description", "gen.revise.description":
			return descriptionPass, nil
		case "gen.body", "gen.revise.body":
			mu.Lock()
			defer mu.Unlock()
			if len(body) == 0 {
				return "", fmt.Errorf("fake: no body response left for %s", r.Role)
			}
			next := body[0]
			if len(body) > 1 {
				body = body[1:]
			}
			return next, nil
		case "suite.evals":
			if evalSet == "" {
				return "", errors.New("fake: no eval set scripted")
			}
			return evalSet, nil
		}
		return "", fmt.Errorf("fake: unscripted role %q", r.Role)
	})
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

// TestRunNew_ALintFailureStillFailsAndStillKeepsTheEvalSet pins the shape of
// the parallel draft: the eval set reads the approved spec, not the bundle, so
// it is drafted beside the bundle and kept. The lint gate still fails the run,
// and a person repairing the bundle keeps the drafted set.
func TestRunNew_ALintFailureStillFailsAndStillKeepsTheEvalSet(t *testing.T) {
	st := newTestStore(t)
	g := newFake(t, []string{brokenBody}, evalSetDraft)

	err := runNew(context.Background(), st, g, "extract tables from PDFs")
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
	if _, lerr := suite.LoadEvalSet(suiteDir); lerr != nil {
		t.Errorf("the drafted eval set was not kept: %v", lerr)
	}
}

// TestRunNew_WritesTheSidecarWhenTheBundleLintsClean is the positive control
// for the test above: with the only difference being a bundle that lints, the
// eval set must appear.
func TestRunNew_WritesTheSidecarWhenTheBundleLintsClean(t *testing.T) {
	st := newTestStore(t)
	g := newFake(t, []string{cleanBody}, evalSetDraft)

	if err := runNew(context.Background(), st, g, "extract tables from PDFs"); err != nil {
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

// TestRunNew_GenerationFailureSuggestsResume pins the resume hint from
// wrapGenerationError: completed passes are cached, so a generation failure
// should name the exact command to pick up from there rather than leaving the
// operator to already know that.
func TestRunNew_GenerationFailureSuggestsResume(t *testing.T) {
	st := newTestStore(t)
	// No body response is scripted, so the body pass fails.
	g := newFake(t, nil, evalSetDraft)

	err := runNew(context.Background(), st, g, "extract tables from PDFs")
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
	g := newFake(t, []string{cleanBody}, "")

	err := runNew(context.Background(), st, g, "extract tables from PDFs")
	if err == nil {
		t.Fatal("runNew succeeded with no suite-draft response scripted")
	}
	if !strings.Contains(err.Error(), "resume with") || !strings.Contains(err.Error(), "whetstone suite gen pdf-extract") {
		t.Errorf("error does not suggest the suite resume command: %v", err)
	}
}

// TestRunNew_WritesAllThreeArtifactsWithNoPrompt pins the creation contract:
// one run, three artifacts, no question asked. The gate this replaces left a
// stored spec and no bundle when a person answered N, which is a half-created
// skill.
func TestRunNew_WritesAllThreeArtifactsWithNoPrompt(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	g := newFake(t, []string{cleanBody}, evalSetDraft)

	if err := runNew(context.Background(), st, g, "extract tables from pdfs"); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	skillDir, err := st.SkillDir("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md: %v", err)
	}

	suiteDir, err := st.SuiteDir("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := suite.LoadEvalSet(suiteDir); err != nil {
		t.Errorf("evals.json: %v", err)
	}
	qs, err := suite.LoadTriggerQueries(suiteDir)
	if err != nil {
		t.Errorf("triggers.json: %v", err)
	}
	if len(qs) == 0 {
		t.Error("triggers.json is empty; the spec's trigger phrases did not reach it")
	}
}

// TestRunNew_ApprovesTheSpecItDrafts guards the downstream consequence.
// RunEvalWith refuses an unapproved spec. A creation run that stores one
// without approval ends with a skill nobody can score.
func TestRunNew_ApprovesTheSpecItDrafts(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if err := runNew(context.Background(), st, newFake(t, []string{cleanBody}, evalSetDraft), "extract tables from pdfs"); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	_, version, err := st.LoadSpec("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if !isApproved(st, "pdf-extract", version) {
		t.Errorf("spec version %d is not approved", version)
	}
}
