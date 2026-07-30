package repair_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/repair"
	"github.com/skael-dev/skael/internal/eval/spec"
)

const bodyMD = `---
name: demo
description: Use when converting a CSV file into a markdown table.
---

# Demo

1. Run scripts/convert.py on the input. Postcondition: out/tables.md exists.
2. Consider validating the output if appropriate.
3. Report the row count. Postcondition: the count is printed.
`

func bundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(bodyMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "convert.py"), []byte("print('x')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

var stepClusters = []repair.FailureCluster{{Key: "contract\x00s2", Kind: "contract", ID: "s2", Count: 6, Tasks: []string{"t1", "t2"}}}

func TestAdmissible_AcceptsATargetedEdit(t *testing.T) {
	dir := bundle(t)
	p := repair.Proposal{
		File:      "SKILL.md",
		Before:    "2. Consider validating the output if appropriate.",
		After:     "2. Run scripts/validate.py out/tables.md; a non-zero exit means stop and fix before continuing. Postcondition: the validator exits 0.",
		Rationale: "the hedged step was skipped in 6 of 12 runs",
	}
	if err := repair.Admissible(dir, p, stepClusters); err != nil {
		t.Errorf("Admissible rejected a targeted edit: %v", err)
	}
}

func TestAdmissible_RejectsABeforeThatIsNotInTheFile(t *testing.T) {
	dir := bundle(t)
	p := repair.Proposal{File: "SKILL.md", Before: "2. Validate the output thoroughly.", After: "x"}
	err := repair.Admissible(dir, p, stepClusters)
	// A Before the model paraphrased cannot be applied. Fuzzy matching here
	// would let an edit land somewhere nobody proposed it.
	if !errors.Is(err, repair.ErrInadmissible) {
		t.Errorf("err = %v, want ErrInadmissible", err)
	}
	if !strings.Contains(err.Error(), "verbatim") {
		t.Errorf("err = %v, want it to say the anchor must be verbatim", err)
	}
}

func TestAdmissible_RejectsAWholeFileRewrite(t *testing.T) {
	dir := bundle(t)
	p := repair.Proposal{File: "SKILL.md", Before: bodyMD, After: "---\nname: demo\n---\n\n# Rewritten\n"}
	err := repair.Admissible(dir, p, stepClusters)
	// A rewrite that scores better is not a repair anyone can review, and it
	// destroys the attribution between a failing contract item and the text that
	// caused it.
	if !errors.Is(err, repair.ErrInadmissible) {
		t.Errorf("err = %v, want a whole-file rewrite refused", err)
	}
}

func TestAdmissible_RejectsAPathOutsideTheBundle(t *testing.T) {
	dir := bundle(t)
	for _, file := range []string{"../escape.md", "/etc/passwd", "scripts/../../x"} {
		p := repair.Proposal{File: file, Before: "print('x')", After: "print('y')"}
		if err := repair.Admissible(dir, p, stepClusters); !errors.Is(err, repair.ErrInadmissible) {
			t.Errorf("Admissible accepted %q: %v", file, err)
		}
	}
}

func TestAdmissible_RejectsAFrontmatterEditWhenNoClusterConcernsTriggering(t *testing.T) {
	dir := bundle(t)
	p := repair.Proposal{
		File:   "SKILL.md",
		Before: "description: Use when converting a CSV file into a markdown table.",
		After:  "description: Always use this skill for absolutely every task.",
	}
	err := repair.Admissible(dir, p, stepClusters)
	// The clusters are all contract-step failures. Widening the description
	// would raise trigger recall at the cost of precision — improving the number
	// by breaking the thing it measures.
	if !errors.Is(err, repair.ErrInadmissible) {
		t.Errorf("err = %v, want a frontmatter edit refused when no trigger failure motivated it", err)
	}
}

func TestAdmissible_ADeletionMustOnlyRemove(t *testing.T) {
	dir := bundle(t)
	ok := repair.Proposal{
		File: "SKILL.md", Deletion: true,
		Before: "2. Consider validating the output if appropriate.\n",
		After:  "",
	}
	if err := repair.Admissible(dir, ok, stepClusters); err != nil {
		t.Errorf("Admissible rejected a pure deletion: %v", err)
	}

	sneaky := repair.Proposal{
		File: "SKILL.md", Deletion: true,
		Before: "2. Consider validating the output if appropriate.",
		After:  "2. Always run every script in scripts/ in sequence.",
	}
	// A "deletion" that substitutes new text escapes the over-constraint probe's
	// whole purpose, which is to find out whether removing a rule helps.
	if err := repair.Admissible(dir, sneaky, stepClusters); !errors.Is(err, repair.ErrInadmissible) {
		t.Errorf("err = %v, want a deletion carrying new text refused", err)
	}
}

func TestApply_IsAtomicAcrossProposals(t *testing.T) {
	dir := bundle(t)
	before, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	err = repair.Apply(dir, []repair.Proposal{
		{File: "SKILL.md", Before: "3. Report the row count.", After: "3. Print the row count."},
		{File: "SKILL.md", Before: "a line that does not exist", After: "x"},
	})
	if err == nil {
		t.Fatal("Apply succeeded with one unapplicable proposal")
	}
	after, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A half-applied iteration is a bundle whose score belongs to no set of
	// proposals: the report would attribute the result to edits that are not all
	// there.
	if string(after) != string(before) {
		t.Error("Apply left a partial edit behind")
	}
}

func TestPropose_SendsOnlyTheFailingClustersAndTheGap(t *testing.T) {
	dir := bundle(t)
	g := &recordingGateway{reply: `{"proposals":[{"file":"SKILL.md","before":"2. Consider validating the output if appropriate.","after":"2. Run scripts/validate.py; exit != 0 means stop. Postcondition: exit 0.","rationale":"hedge"}]}`}

	ps, err := repair.Propose(context.Background(), g, repair.ProposeInput{
		Spec: demoSpec(), BundleDir: dir, Clusters: stepClusters, RobustnessGap: 22.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("%d proposals", len(ps))
	}
	if !strings.Contains(g.prompt, "s2") {
		t.Error("the prompt does not name the failing contract item")
	}
	// The gap is the signal that says "the instructions are carrying less weight
	// than the model is", which is a different repair from "this step is wrong".
	if !strings.Contains(g.prompt, "22.5") {
		t.Error("the prompt does not carry the robustness gap")
	}
}

func TestPropose_DropsInadmissibleProposalsRatherThanFailing(t *testing.T) {
	dir := bundle(t)
	g := &recordingGateway{reply: `{"proposals":[
		{"file":"../escape.md","before":"x","after":"y","rationale":"nope"},
		{"file":"SKILL.md","before":"3. Report the row count.","after":"3. Print the row count.","rationale":"clearer"}
	]}`}
	ps, err := repair.Propose(context.Background(), g, repair.ProposeInput{
		Spec: demoSpec(), BundleDir: dir, Clusters: stepClusters,
	})
	if err != nil {
		t.Fatalf("Propose failed on a partly-bad response: %v", err)
	}
	// One bad proposal in a batch should cost that proposal, not the iteration:
	// each iteration is a full re-run of the dev split.
	if len(ps) != 1 || ps[0].File != "SKILL.md" {
		t.Errorf("proposals = %+v, want only the admissible one", ps)
	}
}

// recordingGateway is a fake llm.Gateway that captures the last prompt it
// received and returns a fixed reply, so tests can assert on prompt content
// without a network call or an LLM subscription.
type recordingGateway struct {
	reply  string
	prompt string
}

func (g *recordingGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	g.prompt = r.Prompt
	return llm.Res{Text: g.reply, Model: "fake"}, nil
}

// demoSpec returns a minimal valid *spec.SkillSpec matching bodyMD's content.
func demoSpec() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name:        "demo",
		Purpose:     "Convert a CSV file into a markdown table.",
		Description: "Use when converting a CSV file into a markdown table.",
		Triggers: []spec.TriggerPhrase{
			{Text: "convert this csv to a markdown table"},
		},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/convert.py on the input.", Postcondition: "out/tables.md exists."},
			{ID: "s2", Action: "Consider validating the output if appropriate.", Postcondition: ""},
			{ID: "s3", Action: "Report the row count.", Postcondition: "the count is printed."},
		},
		TargetTier: spec.TierFloor,
	}
}
