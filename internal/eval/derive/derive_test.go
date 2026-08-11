package derive_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/derive"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	skillpkg "github.com/skael-dev/skael/internal/skill"
)

// derivedEvalCount mirrors derive's unexported evalCount: the fake gateway
// must draft as many evals as Derive asks for, or BuildPlan is checked
// against the wrong denominator.
const derivedEvalCount = 14

// fakeGateway answers spec.Recover with a spec and suite.Generate with an
// eval set, routing on the request's Role rather than on call order — the two
// calls are made in sequence, but routing on Role survives a reordering.
type fakeGateway struct{ evals func() string }

func (g *fakeGateway) ModelFor(llm.ModelClass) string { return "fake-model" }

func (g *fakeGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	if strings.HasPrefix(r.Role, "suite.") {
		return llm.Res{Text: g.evals(), Model: "fake-model"}, nil
	}
	return llm.Res{Text: recoveredSpec, Model: "fake-model"}, nil
}

const recoveredSpec = `{
  "name": "demo",
  "purpose": "Do demo things.",
  "description": "Does demo things. Use when the user mentions demos.",
  "triggers": [{"text": "do demo thing 1"}, {"text": "do demo thing 2"}, {"text": "do demo thing 3"}, {"text": "do demo thing 4"}, {"text": "do an adjacent thing 1", "negative": true}, {"text": "do an adjacent thing 2", "negative": true}, {"text": "do an adjacent thing 3", "negative": true}, {"text": "do an adjacent thing 4", "negative": true}],
  "steps": [{"id": "s1", "action": "Run scripts/run.sh", "postcondition": "out/demo.txt exists"}],
  "target_tier": "mid"
}`

// evalsJSON drafts n evals; the first missing many gives a caller a way to
// make some of them void.
func evalsJSON(n int, expectations func(i int) []string) string {
	type ev struct {
		Prompt         string   `json:"prompt"`
		ExpectedOutput string   `json:"expected_output"`
		Expectations   []string `json:"expectations"`
	}
	var out struct {
		Evals []ev `json:"evals"`
	}
	for i := 1; i <= n; i++ {
		out.Evals = append(out.Evals, ev{
			Prompt:         fmt.Sprintf("Do demo thing %d.", i),
			ExpectedOutput: "it is done",
			Expectations:   expectations(i),
		})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func allPass(int) []string { return []string{"it did the thing"} }

func newTestDeriver(t *testing.T, evals func() string) *derive.Deriver {
	t.Helper()
	d, err := derive.New(derive.Options{Gateway: &fakeGateway{evals: evals}})
	if err != nil {
		t.Fatalf("derive.New: %v", err)
	}
	return d
}

// fixtureBundle packs a minimal published bundle: a SKILL.md and one scripts/
// file, the shape spec.Recover reads from.
func fixtureBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: demo\ndescription: A demo skill for derive tests.\n---\n\n"+
		"Does demo things. Run scripts/run.sh.\n")
	writeFile(t, dir, "scripts/run.sh", "#!/bin/bash\necho demo\n")

	archive, _, _, err := skillpkg.Pack(dir)
	if err != nil {
		t.Fatalf("skill.Pack: %v", err)
	}
	return archive
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestDerive_ProducesAnArchiveChecksAndSpec(t *testing.T) {
	d := newTestDeriver(t, func() string { return evalsJSON(derivedEvalCount, allPass) })
	res, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(res.Archive) == 0 {
		t.Fatal("no archive returned")
	}
	// An eval set with no recorded checks cannot tell a broken eval from a
	// broken skill, and evalsuite.Put refuses it.
	if len(res.Checks) != derivedEvalCount {
		t.Fatalf("%d checks returned, want %d", len(res.Checks), derivedEvalCount)
	}
	if res.Spec == nil || res.Spec.Name != "demo" {
		t.Fatalf("spec = %+v, want one named demo", res.Spec)
	}
}

// The archive must carry both files, in Anthropic's layout, or nothing
// downstream can load it.
func TestDerive_PacksEvalsAndTriggersInTheAnthropicLayout(t *testing.T) {
	d := newTestDeriver(t, func() string { return evalsJSON(derivedEvalCount, allPass) })
	res, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	out := t.TempDir()
	if err := evalsuite.Unpack(res.Archive, out); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	set, err := suite.LoadEvalSet(out)
	if err != nil {
		t.Fatalf("LoadEvalSet: %v", err)
	}
	if len(set.Evals) != derivedEvalCount {
		t.Errorf("%d evals in the archive, want %d", len(set.Evals), derivedEvalCount)
	}
	qs, err := suite.LoadTriggerQueries(out)
	if err != nil {
		t.Fatalf("LoadTriggerQueries: %v", err)
	}
	// The spec fixture carries four positive phrases and four negatives.
	if len(qs) != 8 {
		t.Errorf("%d trigger queries, want the spec's 8", len(qs))
	}
}

// An eval with nothing to grade is void, and a set too thin to plan is
// refused before it is pushed and an eval row created.
func TestDerive_RefusesASetBuildPlanWouldReject(t *testing.T) {
	noExpectations := func(int) []string { return nil }
	d := newTestDeriver(t, func() string { return evalsJSON(derivedEvalCount, noExpectations) })

	_, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	})
	if err == nil {
		t.Fatal("Derive pushed an eval set with nothing to grade")
	}
	// The error is the only record of which evals were void and why: no set is
	// packed, so there are no stored checks for a reader to consult.
	if !strings.Contains(err.Error(), "no expectations") {
		t.Errorf("err = %v, want it to name why each eval is void", err)
	}
}

// Voids are tolerated as long as enough evals survive to fill the tier.
func TestDerive_AcceptsASetWithSomeVoidEvals(t *testing.T) {
	someVoid := func(i int) []string {
		if i <= 3 {
			return nil
		}
		return []string{"it did the thing"}
	}
	d := newTestDeriver(t, func() string { return evalsJSON(derivedEvalCount, someVoid) })

	res, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	void := 0
	for _, c := range res.Checks {
		if c.Void {
			void++
		}
	}
	if void != 3 {
		t.Errorf("%d void checks, want 3", void)
	}
}

func TestNew_RequiresAGateway(t *testing.T) {
	if _, err := derive.New(derive.Options{}); err == nil {
		t.Error("derive.New accepted a nil gateway")
	}
}
