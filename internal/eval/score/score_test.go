package score_test

import (
	"math"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func TestDefaultExponents_SumToOne(t *testing.T) {
	e := score.DefaultExponents
	sum := e.Trigger + e.Reliability + e.Uplift + e.Efficiency
	// Effectiveness is a weighted geometric mean scaled to 100. Exponents that
	// do not sum to 1 make the maximum something other than 100, which silently
	// changes what every threshold in the product means.
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("exponents sum to %v, want 1", sum)
	}
	// The distribution itself: trigger and reliability dominate because a skill
	// that does not fire, or does not work when it does, is worth nothing
	// whatever else it achieves.
	if e.Trigger != 0.35 || e.Reliability != 0.35 || e.Uplift != 0.20 || e.Efficiency != 0.10 {
		t.Errorf("exponents = %+v", e)
	}
}

func TestEffectiveness_AllPillarsPerfectIsOneHundred(t *testing.T) {
	got, err := score.Effectiveness(score.Pillars{1, 1, 1, 1}, score.DefaultExponents)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-100) > 1e-6 {
		t.Errorf("Effectiveness = %v, want 100", got)
	}
}

func TestEffectiveness_AZeroedPillarZeroesTheScore(t *testing.T) {
	// This is the entire reason the mean is geometric. An arithmetic mean would
	// let a skill that never fires still score 65 on the strength of the other
	// three pillars.
	for _, p := range []score.Pillars{
		{TriggerF1: 0, Reliability: 1, Uplift: 1, Efficiency: 1},
		{TriggerF1: 1, Reliability: 0, Uplift: 1, Efficiency: 1},
		{TriggerF1: 1, Reliability: 1, Uplift: 0, Efficiency: 1},
		{TriggerF1: 1, Reliability: 1, Uplift: 1, Efficiency: 0},
	} {
		got, err := score.Effectiveness(p, score.DefaultExponents)
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Errorf("Effectiveness(%+v) = %v, want 0", p, got)
		}
	}
}

func TestEffectiveness_RejectsAPillarOutsideItsRange(t *testing.T) {
	if _, err := score.Effectiveness(score.Pillars{1.2, 1, 1, 1}, score.DefaultExponents); err == nil {
		t.Error("Effectiveness accepted a pillar above 1")
	}
	if _, err := score.Effectiveness(score.Pillars{-0.1, 1, 1, 1}, score.DefaultExponents); err == nil {
		t.Error("Effectiveness accepted a negative pillar")
	}
}

func TestEfficiency_NoOverheadIsOneAndBloatIsFloored(t *testing.T) {
	cases := []struct{ skill, base, want float64 }{
		{100, 200, 1},    // cheaper than baseline: still 1, not a bonus
		{200, 200, 1},    // o = 1
		{400, 200, 0.5},  // o = 2 → 1/o
		{4000, 200, 0.3}, // o = 20 → floored
	}
	for _, c := range cases {
		got, err := score.Efficiency(c.skill, c.base)
		if err != nil {
			t.Fatalf("Efficiency(%v,%v): %v", c.skill, c.base, err)
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Efficiency(%v,%v) = %v, want %v", c.skill, c.base, got, c.want)
		}
	}
	// The floor exists so bloat can flag without annihilating: at exponent 0.10
	// a floored 0.3 costs about 11% of the score, which is a warning rather than
	// a verdict.
	if _, err := score.Efficiency(100, 0); err == nil {
		t.Error("Efficiency accepted a zero baseline")
	}
}

func TestUpliftFromJudge_IsAWinRateWithTiesAtAHalf(t *testing.T) {
	got, err := score.UpliftFromJudge([]score.Verdict{
		{Winner: "skill"}, {Winner: "skill"}, {Winner: "tie"}, {Winner: "baseline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// (1 + 1 + 0.5 + 0) / 4
	if math.Abs(got-0.625) > 1e-9 {
		t.Errorf("Uplift = %v, want 0.625", got)
	}
	if _, err := score.UpliftFromJudge(nil); err == nil {
		t.Error("UpliftFromJudge returned a value with no verdicts")
	}
}

func TestUpliftFromPassRates_IsTheDocumentedFallback(t *testing.T) {
	// 0.5 + (skill − baseline)/2, clamped. This is the degrade path when κ is
	// below the floor: it is a worse measurement than a judge, and it is
	// recorded as such rather than substituted silently.
	if got := score.UpliftFromPassRates(1, 0); math.Abs(got-1) > 1e-9 {
		t.Errorf("got %v, want 1", got)
	}
	if got := score.UpliftFromPassRates(0.5, 0.5); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("got %v, want 0.5", got)
	}
	if got := score.UpliftFromPassRates(0, 1); got != 0 {
		t.Errorf("got %v, want 0 after clamping", got)
	}
}

func TestPanel_HeadlineIsTheMinimumOverHealthyMembers(t *testing.T) {
	m := score.Matrix{Entries: []score.PanelEntry{
		{Member: score.Member{Agent: "claude-code", Model: "opus", Class: spec.TierStrong}, Effectiveness: 82, Healthy: true},
		{Member: score.Member{Agent: "claude-code", Model: "haiku", Class: spec.TierFloor}, Effectiveness: 54, Healthy: true},
	}}
	got, err := m.Headline()
	if err != nil {
		t.Fatal(err)
	}
	// min, not mean: the claim a score makes is "this works", and it only works
	// if it works on the weakest model anyone will run it on.
	if got != 54 {
		t.Errorf("Headline = %v, want 54", got)
	}
	if !m.Complete() {
		t.Error("Complete = false with every member healthy")
	}
}

func TestPanel_AnUnhealthyMemberIsExcludedAndMarksThePanelIncomplete(t *testing.T) {
	m := score.Matrix{Entries: []score.PanelEntry{
		{Member: score.Member{Model: "opus", Class: spec.TierStrong}, Effectiveness: 82, Healthy: true},
		{Member: score.Member{Model: "haiku", Class: spec.TierFloor}, Healthy: false, Detail: "auth expired"},
	}}
	got, err := m.Headline()
	if err != nil {
		t.Fatal(err)
	}
	// An expired token must not read as a skill that scores zero on the floor
	// model. min-gating would convert that into a publish block — infrastructure
	// flakiness presented as a quality verdict.
	if got != 82 {
		t.Errorf("Headline = %v, want the healthy member's 82", got)
	}
	if m.Complete() {
		t.Error("Complete = true with an unhealthy member")
	}
}

func TestPanel_NoHealthyMemberIsAnErrorNotAZero(t *testing.T) {
	m := score.Matrix{Entries: []score.PanelEntry{{Healthy: false, Detail: "CLI not found"}}}
	_, err := m.Headline()
	if err == nil {
		t.Fatal("Headline returned a number for a panel with nothing in it")
	}
	// The reason has to travel: "no score" and "no score because your CLI is
	// broken" lead to different actions.
	if !strings.Contains(err.Error(), "CLI not found") {
		t.Errorf("err = %v, want it to carry the members' details", err)
	}
}

func TestByClass_ExactlyOneMatchIsReturned(t *testing.T) {
	m := score.Matrix{Entries: []score.PanelEntry{
		{Member: score.Member{Model: "opus", Class: spec.TierStrong}, Effectiveness: 82, Healthy: true},
		{Member: score.Member{Model: "haiku", Class: spec.TierFloor}, Effectiveness: 54, Healthy: true},
	}}
	got, ok := m.ByClass(spec.TierStrong)
	if !ok {
		t.Fatal("ByClass = false, want true for exactly one match")
	}
	if got.Member.Model != "opus" {
		t.Errorf("ByClass returned %+v, want the opus member", got)
	}
}

func TestByClass_NoMatchIsNotFound(t *testing.T) {
	m := score.Matrix{Entries: []score.PanelEntry{
		{Member: score.Member{Model: "opus", Class: spec.TierStrong}, Effectiveness: 82, Healthy: true},
	}}
	if _, ok := m.ByClass(spec.TierFloor); ok {
		t.Error("ByClass = true, want false with no matching member")
	}
}

func TestByClass_MultipleMatchesAreNotFound(t *testing.T) {
	// "The strong member" is not defined when there are two of them: a
	// robustness gap computed from whichever happened to be first would be a
	// number with no defined meaning, indistinguishable from a real one. This
	// must fail closed, the same way Headline errors rather than returning
	// zero when no member is healthy.
	m := score.Matrix{Entries: []score.PanelEntry{
		{Member: score.Member{Agent: "claude-code", Model: "opus", Class: spec.TierStrong}, Effectiveness: 82, Healthy: true},
		{Member: score.Member{Agent: "codex", Model: "opus", Class: spec.TierStrong}, Effectiveness: 90, Healthy: true},
	}}
	if _, ok := m.ByClass(spec.TierStrong); ok {
		t.Error("ByClass = true, want false with two matching members of the same class")
	}
}

func TestBootstrap_IsDeterministicForASeedAndBracketsTheMean(t *testing.T) {
	samples := []float64{70, 72, 68, 75, 71, 69, 73, 74}
	lo1, hi1, err := score.Bootstrap(samples, 1000, 7)
	if err != nil {
		t.Fatal(err)
	}
	lo2, hi2, err := score.Bootstrap(samples, 1000, 7)
	if err != nil {
		t.Fatal(err)
	}
	// A confidence interval that changes between two readings of the same data
	// is not a confidence interval anyone can quote.
	if lo1 != lo2 || hi1 != hi2 {
		t.Errorf("bootstrap is not deterministic for a fixed seed: [%v,%v] vs [%v,%v]", lo1, hi1, lo2, hi2)
	}
	if !(lo1 < 71.5 && hi1 > 71.5) {
		t.Errorf("interval [%v,%v] does not contain the sample mean", lo1, hi1)
	}
	if _, _, err := score.Bootstrap(nil, 1000, 7); err == nil {
		t.Error("Bootstrap returned an interval for no samples")
	}
}

func TestMedian_HandlesBothParities(t *testing.T) {
	if m, _ := score.Median([]float64{3, 1, 2}); m != 2 {
		t.Errorf("median = %v, want 2", m)
	}
	if m, _ := score.Median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Errorf("median = %v, want 2.5", m)
	}
	if _, err := score.Median(nil); err == nil {
		t.Error("Median returned a value for no samples")
	}
}
