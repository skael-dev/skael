package whetstone

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/score"
)

// TestTaskReliability_ClampsKToTheTasksOwnN is the regression test for the
// "one errored run aborts the entire eval" defect: score.Reliability refuses
// outright when any task's N is below k (score.PassAtK: "k > n"), and before
// the clamp that refusal propagated straight out of RunEvalWith and killed
// the whole panel over one infrastructure failure on one task. taskReliability
// must degrade that task's own estimate instead.
func TestTaskReliability_ClampsKToTheTasksOwnN(t *testing.T) {
	ts := []score.TaskPasses{
		{TaskID: "t00", N: 2, C: 2}, // both runs measured and passed
		{TaskID: "t01", N: 1, C: 1}, // one run errored out of 2 planned; N dropped to 1
	}
	got, err := taskReliability(ts, 2) // Full tier's skill K
	if err != nil {
		t.Fatalf("taskReliability: %v", err)
	}
	// t00: PassAtK(2,2,2) = 1. t01 clamped to k=1: PassAtK(1,1,1) = 1. Mean = 1.
	if got != 1 {
		t.Errorf("got %v, want 1 (both tasks fully explained by their own surviving runs)", got)
	}
}

// TestTaskReliability_BaselineOwnRunCount is the regression test for the
// CRITICAL defect: scoring the baseline condition with the skill's own K
// (Full: 2) against BaselineRuns (Full: 1) made score.PassAtK refuse with
// "k > n", the refusal was swallowed by a `berr == nil` check, and
// baselinePassRate silently stayed equal to the skill's own reliability —
// pinning UpliftFromPassRates(r, r) at exactly 0.5 on every Full and Deep
// eval, regardless of what the baseline runs actually measured. This
// constructs the asymmetric case that bug hides: a skill that always passes
// against a baseline that always fails should score a real, non-neutral
// Uplift, not the constant produced by comparing reliability to itself.
func TestTaskReliability_BaselineOwnRunCount(t *testing.T) {
	skillTasks := []score.TaskPasses{{TaskID: "t00", N: 2, C: 2}}
	baselineTasks := []score.TaskPasses{{TaskID: "t00", N: 1, C: 0}}

	const fullK, fullBaselineK = 2, 1 // runner.budgets[TierFull].K / .BaselineK

	reliability, err := taskReliability(skillTasks, fullK)
	if err != nil {
		t.Fatalf("skill taskReliability: %v", err)
	}
	if reliability != 1 {
		t.Fatalf("skill reliability = %v, want 1", reliability)
	}

	baselinePassRate, err := taskReliability(baselineTasks, fullBaselineK)
	if err != nil {
		t.Fatalf("baseline taskReliability: %v", err)
	}
	if baselinePassRate != 0 {
		t.Fatalf("baselinePassRate = %v, want 0 (the baseline run failed); the k>n refusal must not have been swallowed into reliability's own value", baselinePassRate)
	}

	uplift := score.UpliftFromPassRates(reliability, baselinePassRate)
	if uplift == 0.5 {
		t.Error("Uplift = 0.5 (neutral); the bug this guards produces exactly this constant by comparing reliability to itself")
	}
	if uplift <= 0.5 {
		t.Errorf("Uplift = %v, want > 0.5: the skill passed every run and the baseline passed none", uplift)
	}

	// The pre-fix call site passed plan.K (2) straight to score.Reliability
	// against a 1-run baseline task and swallowed the k>n refusal. Confirm
	// that refusal is real, so a future regression that reintroduces
	// score.Reliability(baselineTasks, fullK) here is caught by this test
	// rather than by silently reproducing the constant-0.5 bug.
	if _, err := score.Reliability(baselineTasks, fullK); err == nil {
		t.Fatal("score.Reliability(baselineTasks, fullK) did not refuse; the fixture no longer demonstrates the bug this test guards against")
	}
}
