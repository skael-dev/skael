package whetstone

import (
	"context"
	"os"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// suiteExpandGateway answers suite.Generate with a two-eval set. The fake
// gateway is how this runs with no subscription, no API key, and no network.
func suiteExpandGateway() *fake.Gateway {
	return fake.NewFunc(func(llm.Req) (string, error) {
		return `{"evals": [
		  {"prompt": "extract the tables", "expected_output": "a csv",
		   "expectations": ["out/tables.csv exists"]},
		  {"prompt": "pull the tables out", "expected_output": "a csv",
		   "expectations": ["out/tables.csv exists", "the csv has a header row"]}
		]}`, nil
	})
}

// TestNewPipelineWritesTheEvalSidecar covers the two steps of `new` that no
// other test and no manual run reaches without a live gateway: compiling the
// drift contract into the sidecar, and drafting, splitting, and writing the
// suite beside it. Both write through the store's own path helpers, so a
// helper returning an error for a name — or a caller composing a path itself —
// shows up here.
func TestNewPipelineWritesTheEvalSidecar(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	sp := &spec.SkillSpec{
		Name:        "pdf-extract",
		Purpose:     "Extract tables from PDFs.",
		Description: "Extracts tables from PDF files into CSV. Use when the user mentions a PDF.",
		Triggers:    []spec.TriggerPhrase{{Text: "extract this PDF"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/extract.py", Postcondition: "out/tables.csv exists"},
		},
		TargetTier: spec.TierMid,
	}

	if err := writeContract(st, sp); err != nil {
		t.Fatalf("writeContract: %v", err)
	}

	contractPath, err := st.ContractPath(sp.Name)
	if err != nil {
		t.Fatalf("ContractPath: %v", err)
	}
	f, err := os.Open(contractPath)
	if err != nil {
		t.Fatalf("contract was not written where the store says it lives: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := contract.Load(f); err != nil {
		t.Errorf("the written contract does not load back: %v", err)
	}

	if err := generateSuite(context.Background(), st, suiteExpandGateway(), sp); err != nil {
		t.Fatalf("generateSuite: %v", err)
	}

	suiteDir, err := st.SuiteDir(sp.Name)
	if err != nil {
		t.Fatalf("SuiteDir: %v", err)
	}
	loaded, err := suite.LoadEvalSet(suiteDir)
	if err != nil {
		t.Fatalf("the written eval set does not load back: %v", err)
	}
	if len(loaded.Evals) != 2 {
		t.Errorf("loaded %d evals, want 2", len(loaded.Evals))
	}
	for _, ev := range loaded.Evals {
		if len(ev.Expectations) == 0 {
			t.Errorf("eval %d was written with nothing to grade", ev.ID)
		}
	}

	// The trigger queries are written beside the evals, derived from the
	// spec's own trigger phrases.
	qs, err := suite.LoadTriggerQueries(suiteDir)
	if err != nil {
		t.Fatalf("the written trigger queries do not load back: %v", err)
	}
	if len(qs) != len(sp.Triggers) {
		t.Errorf("loaded %d trigger queries, want the spec's %d", len(qs), len(sp.Triggers))
	}
}
