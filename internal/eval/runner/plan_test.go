package runner_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// evals builds a set of n scoreable evals.
func evals(n int) *suite.EvalSet {
	set := &suite.EvalSet{SkillName: "demo"}
	for i := 1; i <= n; i++ {
		set.Evals = append(set.Evals, suite.Eval{
			ID:           i,
			Prompt:       fmt.Sprintf("please do thing %d", i),
			Expectations: []string{"it did the thing"},
		})
	}
	return set
}

// triggers builds n should-trigger and n should-not-trigger queries.
func triggers(n int) []suite.TriggerQuery {
	var out []suite.TriggerQuery
	for i := 0; i < n; i++ {
		out = append(out, suite.TriggerQuery{Query: fmt.Sprintf("do thing %d", i), ShouldTrigger: true})
	}
	for i := 0; i < n; i++ {
		out = append(out, suite.TriggerQuery{Query: fmt.Sprintf("do an adjacent thing %d", i)})
	}
	return out
}

// The budget is what makes a tier's cost predictable, so it is asserted rather
// than assumed.
func TestBuildPlan_FullTierMatchesTheDocumentedBudget(t *testing.T) {
	p, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), evals(10), nil, triggers(8))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	skill, baseline := 0, 0
	for _, k := range p.Runs {
		switch k.Condition {
		case runner.CondSkill:
			skill++
		case runner.CondBaseline:
			baseline++
		}
	}
	// 10 evals × 2 attempts on one member, plus one baseline each.
	if skill != 20 || baseline != 10 {
		t.Errorf("skill=%d baseline=%d, want 20 and 10", skill, baseline)
	}
	if len(p.Probes) != 6 {
		t.Errorf("%d probes, want the 6-query trigger smoke check", len(p.Probes))
	}
	if len(p.Evals) != 10 {
		t.Errorf("%d evals planned, want 10", len(p.Evals))
	}
}

// The baseline answers one published question, so it runs on the primary
// member only however wide the panel is.
func TestBuildPlan_BaselineRunsOnThePrimaryMemberOnly(t *testing.T) {
	p, err := runner.BuildPlan(runner.TierDeep, runner.DeepPanel(), evals(16), nil, triggers(8))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	primary := runner.DeepPanel()[0]
	for _, k := range p.Runs {
		if k.Condition != runner.CondBaseline {
			continue
		}
		if k.Agent != primary.Agent || k.Model != primary.Model {
			t.Fatalf("baseline planned on %s/%s, want the primary member", k.Agent, k.Model)
		}
	}
}

func TestBuildPlan_SmokeIsFiveSessionsAndNoBaselineOrProbes(t *testing.T) {
	p, err := runner.BuildPlan(runner.TierSmoke, runner.DefaultPanel(), evals(10), nil, triggers(8))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(p.Runs) != 5 {
		t.Errorf("%d runs, want 5", len(p.Runs))
	}
	if len(p.Probes) != 0 {
		t.Errorf("%d probes, want none in a smoke tier", len(p.Probes))
	}
}

// A void eval is one nothing can be learned from. Planning it would blame a
// broken eval on the skill.
func TestBuildPlan_ExcludesVoidEvals(t *testing.T) {
	void := map[int]bool{2: true, 3: true}
	p, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), evals(12), void, triggers(8))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	for _, k := range p.Runs {
		if k.TaskID == "2" || k.TaskID == "3" {
			t.Errorf("planned void eval %s", k.TaskID)
		}
	}
}

// A score computed over four evals and labelled "full" is a claim nothing
// downstream can check.
func TestBuildPlan_RefusesATierItCannotFill(t *testing.T) {
	_, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), evals(4), nil, triggers(8))
	if err == nil {
		t.Fatal("BuildPlan silently produced a smaller Full tier")
	}
	if !strings.Contains(err.Error(), "10") || !strings.Contains(err.Error(), "4") {
		t.Errorf("err = %v, want it to name the required and available counts", err)
	}
}

// A trigger F1 measured over fewer queries than the tier promises is not the
// measurement the tier's name claims.
func TestBuildPlan_RefusesTooFewTriggerQueries(t *testing.T) {
	_, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), evals(10), nil, triggers(1))
	if err == nil {
		t.Fatal("BuildPlan accepted a trigger set too thin for the tier")
	}
	if !strings.Contains(err.Error(), "triggers.json") {
		t.Errorf("err = %v, want it to name the file to fix", err)
	}
}

func TestBuildPlan_RefusesAnEmptyPanel(t *testing.T) {
	if _, err := runner.BuildPlan(runner.TierFull, nil, evals(10), nil, triggers(8)); err == nil {
		t.Error("BuildPlan accepted an empty panel")
	}
}

// Which evals a tier takes must not depend on the order they were listed in.
func TestBuildPlan_IsDeterministic(t *testing.T) {
	a, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), evals(30), nil, triggers(8))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	shuffled := evals(30)
	for i, j := 0, len(shuffled.Evals)-1; i < j; i, j = i+1, j-1 {
		shuffled.Evals[i], shuffled.Evals[j] = shuffled.Evals[j], shuffled.Evals[i]
	}
	b, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), shuffled, nil, triggers(8))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	if len(a.Runs) != len(b.Runs) {
		t.Fatalf("%d runs against %d for the same set in a different order", len(a.Runs), len(b.Runs))
	}
	for i := range a.Runs {
		if a.Runs[i] != b.Runs[i] {
			t.Fatalf("run %d differs: %+v against %+v", i, a.Runs[i], b.Runs[i])
		}
	}
}

func TestParsePanel_RejectsAnUnknownAgent(t *testing.T) {
	if _, err := runner.ParsePanel([]string{"nonesuch"}, []string{"sonnet"}); err == nil {
		t.Error("ParsePanel accepted an unregistered agent")
	}
}

func TestParsePanel_FirstModelIsStrongAndTheRestAreFloor(t *testing.T) {
	p, err := runner.ParsePanel([]string{"claude-code"}, []string{"sonnet", "haiku"})
	if err != nil {
		t.Fatalf("ParsePanel: %v", err)
	}
	if len(p) != 2 {
		t.Fatalf("panel has %d members, want 2", len(p))
	}
	if p[0].Class != spec.TierStrong || p[1].Class != spec.TierFloor {
		t.Errorf("panel classes = %v, %v, want strong then floor", p[0].Class, p[1].Class)
	}
}

// The default tier scores one member; deep adds the floor.
func TestPanelFor_WidensOnlyAtTheDeepTier(t *testing.T) {
	if got := len(runner.PanelFor(runner.TierFull)); got != 1 {
		t.Errorf("full-tier panel has %d members, want 1", got)
	}
	if got := len(runner.PanelFor(runner.TierDeep)); got != 2 {
		t.Errorf("deep-tier panel has %d members, want 2", got)
	}
}
