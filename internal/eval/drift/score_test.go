package drift_test

import (
	"math"
	"testing"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func near(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func TestDefaultWeights_SumToOne(t *testing.T) {
	w := drift.DefaultWeights
	sum := w.StepCoverage + w.Order + w.Violation + w.Checkpoint + w.Semantic + w.Focus
	// Adherence is 100 × Σ wᵢcᵢ. Weights that do not sum to 1 silently rescale
	// every score in the repository, and every grade boundary with it.
	near(t, "weight sum", sum, 1.0)
}

func TestScore_APerfectRunScoresOneHundred(t *testing.T) {
	o := &drift.Observation{
		Steps:       []drift.StepObs{{ID: "s1", Matched: true, Required: true, OrderOK: true}},
		Checkpoints: map[string]bool{"s1": true},
		Total:       1,
	}
	r, err := drift.Score(o, 1.0, drift.DefaultWeights)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "Adherence", r.Adherence, 100)
	near(t, "Drift", r.Drift, 0)
}

func TestScore_ASingleCriticalViolationDominates(t *testing.T) {
	o := &drift.Observation{
		Steps:       []drift.StepObs{{ID: "s1", Matched: true, Required: true, OrderOK: true}},
		Violations:  []drift.Violation{{ID: "f1", Severity: spec.SeverityCritical, Hits: 1}},
		Checkpoints: map[string]bool{"s1": true},
		Total:       2,
	}
	r, err := drift.Score(o, 1.0, drift.DefaultWeights)
	if err != nil {
		t.Fatal(err)
	}
	// exp(-3) ≈ 0.0498. A critical violation is meant to be close to
	// unrecoverable within its component; a linear penalty would let a skill
	// that exfiltrates once still grade well on volume of correct steps.
	near(t, "ViolationScore", r.Components.Violation, math.Exp(-3))
	if r.Adherence > 82 {
		t.Errorf("Adherence = %v; a critical violation barely moved the score", r.Adherence)
	}
}

func TestScore_SeverityWeightsCompound(t *testing.T) {
	o := &drift.Observation{
		Violations: []drift.Violation{
			{ID: "f1", Severity: spec.SeverityMajor, Hits: 2},
			{ID: "f2", Severity: spec.SeverityMinor, Hits: 1},
		},
		Total: 3,
	}
	r, err := drift.Score(o, 1.0, drift.DefaultWeights)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "ViolationScore", r.Components.Violation, math.Exp(-(2*1.0 + 1*0.3)))
}

func TestScore_NoRequiredStepsIsVacuouslyFullCoverage(t *testing.T) {
	o := &drift.Observation{Total: 3, OffContract: 3}
	r, err := drift.Score(o, 1.0, drift.DefaultWeights)
	if err != nil {
		t.Fatal(err)
	}
	// A contract with no required steps makes no claim about steps. Scoring
	// that as zero coverage would penalise a skill for its specification being
	// entirely semantic, which is a legitimate shape.
	near(t, "StepCoverage", r.Components.StepCoverage, 1)
	near(t, "Order", r.Components.Order, 1)
	near(t, "Checkpoint", r.Components.Checkpoint, 1)
	// Focus still bites: every action was off-contract.
	near(t, "Focus", r.Components.Focus, 0)
}

func TestScore_OrderUsesNormalizedInversions(t *testing.T) {
	// Three ordered steps observed in reverse: 3 of 3 possible inversions.
	o := &drift.Observation{
		Steps: []drift.StepObs{
			{ID: "s1", Matched: true, Required: true, Seq: 3, OrderOK: false},
			{ID: "s2", Matched: true, Required: true, Seq: 2, OrderOK: false},
			{ID: "s3", Matched: true, Required: true, Seq: 1, OrderOK: true},
		},
		Total: 3,
	}
	r, err := drift.Score(o, 1.0, drift.DefaultWeights)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "Order", r.Components.Order, 0)
}

func TestScore_SemanticMustBeARate(t *testing.T) {
	o := &drift.Observation{Total: 1}
	if _, err := drift.Score(o, 1.7, drift.DefaultWeights); err == nil {
		t.Error("Score accepted a semantic component above 1; Adherence would exceed 100")
	}
	if _, err := drift.Score(o, -0.1, drift.DefaultWeights); err == nil {
		t.Error("Score accepted a negative semantic component")
	}
}

func TestGrade_BoundariesAreInclusive(t *testing.T) {
	cases := []struct {
		mean, worst float64
		want        string
	}{
		{90, 80, "A"}, {89.9, 80, "B"}, {90, 79.9, "B"},
		{75, 65, "B"}, {74.9, 65, "C"}, {75, 64.9, "C"},
		{60, 0, "C"}, {59.9, 0, "D"},
	}
	for _, c := range cases {
		if got := drift.Grade(c.mean, c.worst); got != c.want {
			t.Errorf("Grade(%v, %v) = %q, want %q", c.mean, c.worst, got, c.want)
		}
	}
}

func TestAggregate_ReportsWorstAndInstability(t *testing.T) {
	rs := []drift.Result{{Adherence: 90}, {Adherence: 70}, {Adherence: 80}}
	a, err := drift.Aggregate(rs)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "Mean", a.Mean, 80)
	// Worst-run is reported separately because a skill that works four times
	// out of five is a different failure from one that half-works every time,
	// and a mean cannot tell them apart.
	near(t, "Worst", a.Worst, 70)
	near(t, "Sigma", a.Sigma, math.Sqrt(200.0/3.0))

	same, err := drift.Aggregate([]drift.Result{{Adherence: 80}, {Adherence: 80}})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "Sigma of identical runs", same.Sigma, 0)

	// Zero runs is not a zero score. It is an absent measurement, and returning
	// 0 makes it indistinguishable from a skill that failed everything.
	if _, err := drift.Aggregate(nil); err == nil {
		t.Error("Aggregate returned a score for zero runs")
	}
}

func TestRobustnessGap_IsPositiveWhenTheSkillLeansOnTheStrongModel(t *testing.T) {
	gap := drift.RobustnessGap(drift.Agg{Mean: 92, N: 2}, drift.Agg{Mean: 61, N: 2})
	near(t, "gap", gap, 31)
	// The sign convention is load-bearing: repair reads a positive gap as
	// "the instructions are carrying less weight than the model is".
	if drift.RobustnessGap(drift.Agg{Mean: 70, N: 2}, drift.Agg{Mean: 75, N: 2}) >= 0 {
		t.Error("gap is not negative when the floor model adhered better")
	}
}
