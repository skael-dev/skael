package repair_test

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/repair"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// stubEvaluator returns a scripted score per iteration and records which tasks
// it was asked to evaluate.
type stubEvaluator struct {
	dev      []float64
	holdout  float64
	devCalls [][]string
	hdCalls  [][]string
	failures []repair.Failure
	conds    []repair.ConditionResult
	n        int
}

func (e *stubEvaluator) EvaluateDev(_ context.Context, ts []suite.TaskPkg) (*repair.DevResult, error) {
	e.devCalls = append(e.devCalls, ids(ts))
	v := e.dev[min(e.n, len(e.dev)-1)]
	e.n++
	return &repair.DevResult{Effectiveness: v, Failures: e.failures, Conditions: e.conds, RobustnessGap: 20}, nil
}

func (e *stubEvaluator) EvaluateHoldout(_ context.Context, ts []suite.TaskPkg) (*repair.DevResult, error) {
	e.hdCalls = append(e.hdCalls, ids(ts))
	return &repair.DevResult{Effectiveness: e.holdout}, nil
}

func ids(ts []suite.TaskPkg) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func splitSuite() *suite.Suite {
	return &suite.Suite{Tasks: []suite.TaskPkg{
		{ID: "d1", Split: "dev", PromptMD: "dev prompt one"},
		{ID: "d2", Split: "dev", PromptMD: "dev prompt two"},
		{ID: "h1", Split: "holdout", PromptMD: "holdout prompt one"},
	}}
}

func loop(t *testing.T, g *recordingGateway) *repair.Loop {
	t.Helper()
	l, err := repair.NewLoop(repair.Options{Gateway: g, Logger: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestRun_NeverShowsTheRepairPromptAHoldoutTask(t *testing.T) {
	g := &recordingGateway{reply: `{"proposals":[{"file":"SKILL.md","before":"3. Report the row count.","after":"3. Print the row count.","rationale":"clearer"}]}`}
	e := &stubEvaluator{
		dev:      []float64{40, 55, 70},
		holdout:  62,
		failures: []repair.Failure{{Kind: "contract", ID: "s2", TaskID: "d1"}},
	}

	if _, err := loop(t, g).Run(context.Background(), e, demoSpec(), bundle(t), splitSuite()); err != nil {
		t.Fatal(err)
	}
	// A holdout leaking into the prompt is a suite the loop can fit, and the
	// reported score stops being a measurement of anything.
	for _, p := range g.prompts {
		if strings.Contains(p, "h1") || strings.Contains(p, "holdout prompt one") {
			t.Fatalf("the repair prompt contained a holdout task:\n%s", p)
		}
	}
	for _, call := range e.devCalls {
		for _, id := range call {
			if id == "h1" {
				t.Error("a holdout task was evaluated during iteration")
			}
		}
	}
}

func TestRun_ReportsTheHoldoutScoreNotTheDevScore(t *testing.T) {
	g := &recordingGateway{reply: `{"proposals":[{"file":"SKILL.md","before":"3. Report the row count.","after":"3. Print the row count.","rationale":"c"}]}`}
	e := &stubEvaluator{dev: []float64{40, 90}, holdout: 61, failures: []repair.Failure{{Kind: "contract", ID: "s2", TaskID: "d1"}}}

	r, err := loop(t, g).Run(context.Background(), e, demoSpec(), bundle(t), splitSuite())
	if err != nil {
		t.Fatal(err)
	}
	// 90 on the split the loop optimised against is the number that is not
	// evidence. 61 on the split it never saw is.
	if r.Holdout != 61 {
		t.Errorf("Holdout = %v, want 61", r.Holdout)
	}
	if len(e.hdCalls) != 1 {
		t.Errorf("holdout evaluated %d times, want exactly once at the end", len(e.hdCalls))
	}
}

func TestRun_StopsAtAPlateau(t *testing.T) {
	g := &recordingGateway{reply: `{"proposals":[{"file":"SKILL.md","before":"3. Report the row count.","after":"3. Print the row count.","rationale":"c"}]}`}
	e := &stubEvaluator{dev: []float64{40, 41}, holdout: 40, failures: []repair.Failure{{Kind: "contract", ID: "s2", TaskID: "d1"}}}

	r, err := loop(t, g).Run(context.Background(), e, demoSpec(), bundle(t), splitSuite())
	if err != nil {
		t.Fatal(err)
	}
	// A one-point gain for a whole dev-split re-run is not progress worth the
	// sessions.
	if r.Stopped != "plateau" {
		t.Errorf("Stopped = %q, want plateau", r.Stopped)
	}
	if len(r.Iterations) > 2 {
		t.Errorf("%d iterations after a plateau", len(r.Iterations))
	}
}

func TestRun_StopsAtMaxIterations(t *testing.T) {
	g := &recordingGateway{reply: `{"proposals":[{"file":"SKILL.md","before":"3. Report the row count.","after":"3. Print the row count.","rationale":"c"}]}`}
	e := &stubEvaluator{dev: []float64{10, 30, 50, 70, 90}, holdout: 55, failures: []repair.Failure{{Kind: "contract", ID: "s2", TaskID: "d1"}}}

	r, err := loop(t, g).Run(context.Background(), e, demoSpec(), bundle(t), splitSuite())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Iterations) != repair.DefaultMaxIter {
		t.Errorf("%d iterations, want the %d cap even while improving", len(r.Iterations), repair.DefaultMaxIter)
	}
}

func TestRun_PrunesNonDiscriminatingTasksFromLaterIterations(t *testing.T) {
	g := &recordingGateway{reply: `{"proposals":[{"file":"SKILL.md","before":"3. Report the row count.","after":"3. Print the row count.","rationale":"c"}]}`}
	e := &stubEvaluator{
		dev: []float64{40, 60, 80}, holdout: 55,
		failures: []repair.Failure{{Kind: "contract", ID: "s2", TaskID: "d1"}},
		conds: []repair.ConditionResult{
			{TaskID: "d1", Model: "opus", SkillPassed: false, BaselinePassed: true},
			{TaskID: "d2", Model: "opus", SkillPassed: true, BaselinePassed: true},
		},
	}

	r, err := loop(t, g).Run(context.Background(), e, demoSpec(), bundle(t), splitSuite())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.PrunedTasks) != 1 || r.PrunedTasks[0] != "d2" {
		t.Errorf("PrunedTasks = %v, want [d2]", r.PrunedTasks)
	}
	// The prune has to change what runs next, not just what is reported: a
	// pruned task still contributing to the score is a report that disagrees
	// with its own number.
	if len(e.devCalls) < 2 {
		t.Fatal("only one dev evaluation")
	}
	for _, id := range e.devCalls[1] {
		if id == "d2" {
			t.Error("a pruned task was evaluated in the next iteration")
		}
	}
}

func TestRun_RoutesBrokenTasksToTheSuiteAndNotToSkillEdits(t *testing.T) {
	g := &recordingGateway{reply: `{"proposals":[]}`}
	e := &stubEvaluator{
		dev: []float64{40}, holdout: 40,
		conds: []repair.ConditionResult{{TaskID: "d1", Model: "opus", SkillPassed: false, BaselinePassed: false}},
	}

	r, err := loop(t, g).Run(context.Background(), e, demoSpec(), bundle(t), splitSuite())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.BrokenTasks) != 1 || r.BrokenTasks[0] != "d1" {
		t.Errorf("BrokenTasks = %v, want [d1]", r.BrokenTasks)
	}
	// No wording change fixes a task that fails without the skill too. Sending
	// it to the model spends an iteration and produces a plausible edit that
	// changes nothing.
	for _, p := range g.prompts {
		if strings.Contains(p, "d1") {
			t.Errorf("a broken task was sent to the repair prompt:\n%s", p)
		}
	}
}

func TestRun_TriesDeletionOnceScoresPlateau(t *testing.T) {
	g := &recordingGateway{reply: `{"proposals":[{"file":"SKILL.md","before":"2. Consider validating the output if appropriate.\n","after":"","rationale":"over-constrained","deletion":true}]}`}
	e := &stubEvaluator{dev: []float64{40, 40.5, 40.6}, holdout: 41, failures: []repair.Failure{{Kind: "contract", ID: "s2", TaskID: "d1"}}}

	if _, err := loop(t, g).Run(context.Background(), e, demoSpec(), bundle(t), splitSuite()); err != nil {
		t.Fatal(err)
	}
	// An add-only loop cannot escape over-constraint: when adding rules stops
	// helping, the next hypothesis worth testing is that one of them is the
	// problem.
	var sawDeletionOffer bool
	for _, p := range g.prompts {
		if strings.Contains(strings.ToLower(p), "delete") || strings.Contains(strings.ToLower(p), "remov") {
			sawDeletionOffer = true
		}
	}
	if !sawDeletionOffer {
		t.Error("the loop never offered deletion despite a plateau")
	}
}
