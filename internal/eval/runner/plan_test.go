package runner_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// tasks builds a split suite: n tasks, the last holdoutN of them holdout.
func tasks(n, holdoutN int) *suite.Suite {
	s := &suite.Suite{Triggers: suite.TriggerSet{}}
	for i := 0; i < n; i++ {
		split := "dev"
		if i >= n-holdoutN {
			split = "holdout"
		}
		s.Tasks = append(s.Tasks, suite.TaskPkg{ID: fmt.Sprintf("t%02d", i), Kind: "happy", Split: split})
	}
	for i := 0; i < 8; i++ {
		s.Triggers.Positive = append(s.Triggers.Positive, fmt.Sprintf("please do thing %d", i))
		s.Triggers.Negative = append(s.Triggers.Negative, fmt.Sprintf("do an adjacent thing %d", i))
	}
	return s
}

func TestBuildPlan_FullTierMatchesTheDocumentedBudget(t *testing.T) {
	p, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), tasks(10, 3), nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	// 10 tasks × 2 runs × 2 models = 40 skill sessions, plus 10 baselines × 1
	// × 2 models = 20, plus 16 short trigger probes. The budget is what makes a
	// tier's cost predictable, so it is asserted rather than assumed.
	skill, baseline := 0, 0
	for _, k := range p.Runs {
		switch k.Condition {
		case runner.CondSkill:
			skill++
		case runner.CondBaseline:
			baseline++
		}
	}
	if skill != 40 || baseline != 20 {
		t.Errorf("skill=%d baseline=%d, want 40 and 20", skill, baseline)
	}
	if len(p.Probes) != 16 {
		t.Errorf("%d probes, want 16", len(p.Probes))
	}
	if p.N != 2 || p.K != 2 {
		t.Errorf("N=%d K=%d, want 2 and 2 for a Full tier", p.N, p.K)
	}
	// Probes are measured on the primary member only: they answer "does it
	// fire", which is a property of the skill's description, not of the model
	// panel.
	for _, pr := range p.Probes {
		if pr.Member != runner.DefaultPanel()[0] {
			t.Errorf("probe on %+v, want the primary member", pr.Member)
		}
	}
}

func TestBuildPlan_SmokeIsFiveDevSessionsAndNoBaselines(t *testing.T) {
	p, err := runner.BuildPlan(runner.TierSmoke, runner.DefaultPanel(), tasks(10, 3), nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(p.Runs) != 5 {
		t.Errorf("%d runs, want 5", len(p.Runs))
	}
	if len(p.Probes) != 0 {
		t.Errorf("%d probes, want none in a smoke tier", len(p.Probes))
	}
	// A smoke tier is immediate feedback on the development split. Touching
	// holdout here would spend the one measurement that is supposed to stay
	// unseen.
	holdout := map[string]bool{}
	for _, task := range tasks(10, 3).Tasks {
		if task.Split == "holdout" {
			holdout[task.ID] = true
		}
	}
	for _, k := range p.Runs {
		if holdout[k.TaskID] {
			t.Errorf("smoke tier planned holdout task %s", k.TaskID)
		}
	}
}

func TestBuildPlan_ExcludesVoidTasksAndSaysSo(t *testing.T) {
	void := map[string]bool{"t00": true, "t01": true}
	p, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), tasks(12, 3), void)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	for _, k := range p.Runs {
		if void[k.TaskID] {
			t.Errorf("planned void task %s; a broken task would be blamed on the skill", k.TaskID)
		}
	}
}

func TestBuildPlan_RefusesATierItCannotFill(t *testing.T) {
	_, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), tasks(4, 1), nil)
	if err == nil {
		t.Fatal("BuildPlan silently produced a smaller Full tier")
	}
	// A score computed over four tasks and labelled "full" is a claim nothing
	// downstream can check. The shortfall has to be named where it can be fixed.
	if !strings.Contains(err.Error(), "10") || !strings.Contains(err.Error(), "4") {
		t.Errorf("err = %v, want it to name the required and available task counts", err)
	}
}

func TestBuildPlan_RefusesAnEmptyPanel(t *testing.T) {
	if _, err := runner.BuildPlan(runner.TierFull, nil, tasks(10, 3), nil); err == nil {
		t.Error("BuildPlan accepted an empty panel")
	}
}

func TestBuildPlan_IsDeterministic(t *testing.T) {
	a, _ := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), tasks(10, 3), nil)
	b, _ := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), tasks(10, 3), nil)
	// Resume matches a stored run against a planned key. A plan whose order or
	// attempt numbering varies makes every resumed run a fresh one.
	if len(a.Runs) != len(b.Runs) {
		t.Fatalf("plan sizes differ: %d vs %d", len(a.Runs), len(b.Runs))
	}
	for i := range a.Runs {
		if a.Runs[i] != b.Runs[i] {
			t.Fatalf("run %d differs: %+v vs %+v", i, a.Runs[i], b.Runs[i])
		}
	}
}

func TestDefaultPanel_SpansTwoCapabilityTiers(t *testing.T) {
	p := runner.DefaultPanel()
	if len(p) != 2 {
		t.Fatalf("panel has %d members, want 2", len(p))
	}
	// min-across-panel gating and RobustnessGap both need a strong member and a
	// floor member. One vendor is a known limitation; one capability tier would
	// make both metrics undefined.
	seen := map[spec.ModelTier]bool{}
	for _, m := range p {
		seen[m.Class] = true
	}
	if !seen[spec.TierStrong] || !seen[spec.TierFloor] {
		t.Errorf("panel classes = %v, want a strong and a floor member", seen)
	}
}

func TestParsePanel_RejectsAnUnknownAgent(t *testing.T) {
	if _, err := runner.ParsePanel([]string{"nonesuch"}, []string{"opus"}); err == nil {
		t.Error("ParsePanel accepted an unregistered agent")
	}
}
