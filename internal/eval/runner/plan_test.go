package runner_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"

	// Registers "claude-code" and "codex" in the adapter registry so
	// ParsePanel's agent-name cross-check has something real to find.
	_ "github.com/skael-dev/skael/internal/eval/agent/claudecode"
	_ "github.com/skael-dev/skael/internal/eval/agent/codex"
)

// tasks builds a split suite: n tasks, holdoutN of them holdout, spread
// evenly across the ID range rather than clustered at the end. Interleaving
// matters: a fixture where holdout is always the highest-numbered IDs makes a
// plain ID-order prefix look split-aware when it is not, and makes a missing
// split filter invisible to a test that only checks the first few IDs taken.
func tasks(n, holdoutN int) *suite.Suite {
	s := &suite.Suite{Triggers: suite.TriggerSet{}}
	holdout := map[int]bool{}
	for i := 0; i < holdoutN; i++ {
		holdout[i*n/holdoutN] = true
	}
	for i := 0; i < n; i++ {
		split := "dev"
		if holdout[i] {
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
	// BaselineK must fit BaselineRuns (1 for Full), not K: K=2 exceeds the
	// single baseline attempt a Full tier plans, and score.PassAtK refuses
	// k > n.
	if p.BaselineK != 1 {
		t.Errorf("BaselineK=%d, want 1 for a Full tier (BaselineRuns=1)", p.BaselineK)
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

func TestBuildPlan_FullTierReservesHoldoutFromALargerSuite(t *testing.T) {
	// 30 tasks, 9 of them holdout: comfortably more than the Full tier's
	// budget of 10 tasks (7 dev + 3 holdout), so this exercises the
	// stratified selection rather than "every eligible task happens to be
	// used".
	p, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), tasks(30, 9), nil)
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
	if skill != 40 || baseline != 20 {
		t.Errorf("skill=%d baseline=%d, want 40 and 20 regardless of suite size", skill, baseline)
	}
	// The reported score of an eval is the holdout score. A Full tier that
	// matches the documented session count while silently drawing zero
	// holdout tasks would report a number computed from nothing.
	holdoutRuns := p.HoldoutRuns()
	if len(holdoutRuns) == 0 {
		t.Fatal("HoldoutRuns() is empty for a Full tier run against a larger suite")
	}
	distinct := map[string]bool{}
	for _, k := range holdoutRuns {
		distinct[k.TaskID] = true
	}
	if len(distinct) != 3 {
		t.Errorf("%d distinct holdout tasks, want 3 (round(30%% of the Full budget of 10))", len(distinct))
	}
}

func TestBuildPlan_RefusesATierWithTooFewHoldoutTasks(t *testing.T) {
	// 20 tasks is plenty for the Full tier's overall budget of 10, but only
	// one of them is holdout — short of the 3 the Full tier reserves. Filling
	// the shortfall from the dev pool would be exactly the silent-starvation
	// bug being fixed here, so this must be a refusal, not a smaller holdout.
	_, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), tasks(20, 1), nil)
	if err == nil {
		t.Fatal("BuildPlan silently filled a Full tier's holdout share from the dev pool")
	}
	if !strings.Contains(err.Error(), "holdout") {
		t.Errorf("err = %v, want it to name the holdout shortfall", err)
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
	// unseen. With holdout tasks interleaved through the ID range (rather than
	// clustered at the end), this fails if the DevOnly filter is ever dropped.
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
	// t01 and t02 are dev tasks in tasks(12, 3) (holdout is t00, t04, t08);
	// voiding them still leaves exactly the Full tier's dev (7) and holdout
	// (3) shares fillable.
	void := map[string]bool{"t01": true, "t02": true}
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
	// 30 tasks / 9 holdout, against a Full tier's budget of 10 (7 dev + 3
	// holdout): both pools have more than the budget takes, so which tasks
	// get truncated away actually depends on order — a suite with exactly the
	// budget's task count wouldn't exercise that, since taking "all of a
	// pool" is order-independent.
	sa := tasks(30, 9)
	a, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), sa, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	// Same tasks, shuffled input order: a plan that truncates using the
	// suite's original ordering (rather than sorting before it enumerates)
	// would select a different subset here.
	sb := tasks(30, 9)
	rand.New(rand.NewSource(1)).Shuffle(len(sb.Tasks), func(i, j int) {
		sb.Tasks[i], sb.Tasks[j] = sb.Tasks[j], sb.Tasks[i]
	})
	b, err := runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), sb, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

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

func TestParsePanel_RejectsATwoAgentPanelWithOnlyOneModel(t *testing.T) {
	// Two agents, one model: class assignment is keyed to the model's index
	// within each agent's inner loop, so every member here would otherwise
	// land in TierStrong with no floor member at all.
	_, err := runner.ParsePanel([]string{"claude-code", "codex"}, []string{"opus"})
	if err == nil {
		t.Fatal("ParsePanel accepted a panel with no floor member")
	}
	if !strings.Contains(err.Error(), "floor") {
		t.Errorf("err = %v, want it to name the missing floor class", err)
	}
}

func TestParsePanel_OneAgentTwoModelsYieldsAStrongAndAFloorMember(t *testing.T) {
	p, err := runner.ParsePanel([]string{"claude-code"}, []string{"opus", "haiku"})
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
