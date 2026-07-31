package whetstone

import (
	"context"
	"os"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// suiteDraft is one scripted gateway response in suite.Generate's schema. The
// fake gateway is how this runs with no subscription, no API key, and no
// network.
const suiteDraft = `{
  "tasks": [
    {"id": "happy", "kind": "happy", "prompt_md": "extract the tables",
     "oracle": "#!/bin/sh\nexit 0\n", "verifier": "#!/bin/sh\ntest -f out/tables.csv\n"},
    {"id": "variant", "kind": "variant", "prompt_md": "pull the tables out",
     "oracle": "#!/bin/sh\nexit 0\n", "verifier": "#!/bin/sh\ntest -f out/tables.csv\n"}
  ],
  "triggers": {"positive": ["extract this PDF"], "negative": ["extract this zip"]}
}`

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

	if err := generateSuite(context.Background(), st, fake.New(suiteDraft), sp); err != nil {
		t.Fatalf("generateSuite: %v", err)
	}

	suiteDir, err := st.SuiteDir(sp.Name)
	if err != nil {
		t.Fatalf("SuiteDir: %v", err)
	}
	loaded, err := suite.Load(suiteDir)
	if err != nil {
		t.Fatalf("the written suite does not load back: %v", err)
	}
	if len(loaded.Tasks) != 2 {
		t.Errorf("loaded %d tasks, want 2", len(loaded.Tasks))
	}

	// Split must have run before Write: the reported score is the holdout
	// score, and an unsplit suite has no holdout at all.
	var holdout int
	for _, task := range loaded.Tasks {
		if task.Split == "" {
			t.Errorf("task %q was written with no split assigned", task.ID)
		}
		if task.Split == "holdout" {
			holdout++
		}
	}
	if holdout == 0 {
		t.Error("the written suite has no holdout tasks")
	}
}
