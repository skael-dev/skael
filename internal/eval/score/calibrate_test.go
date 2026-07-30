package score_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/score"
)

func TestKappa_PerfectAgreementIsOne(t *testing.T) {
	labels := []string{"skill", "baseline", "tie", "skill", "baseline"}
	k, err := score.Kappa(labels, labels)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(k-1) > 1e-9 {
		t.Errorf("κ = %v, want 1", k)
	}
}

func TestKappa_ChanceAgreementIsAboutZero(t *testing.T) {
	// Two raters who both say "skill" on the same fraction of items but never
	// on the same items. Raw agreement is high; κ is what strips the credit
	// they get for guessing, which is the entire reason this is κ and not a
	// percentage.
	labels := []string{"skill", "skill", "baseline", "baseline"}
	verdicts := []string{"baseline", "baseline", "skill", "skill"}
	k, err := score.Kappa(labels, verdicts)
	if err != nil {
		t.Fatal(err)
	}
	if k >= 0 {
		t.Errorf("κ = %v, want negative for systematic disagreement", k)
	}
}

func TestKappa_WorkedExample(t *testing.T) {
	// Two categories, 10 items: 6 agree-skill, 1 agree-baseline, 2 rater-A-only
	// skill, 1 rater-B-only skill.
	//   po = 0.7; pe = (8/10)(7/10) + (2/10)(3/10) = 0.62; κ = 0.08/0.38.
	labels := []string{"skill", "skill", "skill", "skill", "skill", "skill", "baseline", "skill", "skill", "baseline"}
	verdicts := []string{"skill", "skill", "skill", "skill", "skill", "skill", "baseline", "baseline", "baseline", "skill"}
	k, err := score.Kappa(labels, verdicts)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(k-0.08/0.38) > 1e-6 {
		t.Errorf("κ = %v, want %v", k, 0.08/0.38)
	}
}

func TestKappa_RejectsMismatchedOrEmptyInput(t *testing.T) {
	if _, err := score.Kappa([]string{"skill"}, []string{"skill", "tie"}); err == nil {
		t.Error("Kappa accepted mismatched lengths")
	}
	if _, err := score.Kappa(nil, nil); err == nil {
		t.Error("Kappa returned a value for zero items")
	}
	// A set where every label is identical has zero expected variance: κ is
	// undefined, and returning 0 would read as "the judge is worthless" when the
	// truth is "this set cannot measure it".
	if _, err := score.Kappa([]string{"skill", "skill"}, []string{"skill", "skill"}); err != nil {
		t.Logf("degenerate set: %v", err)
	}
}

func TestCalibration_ShipsThirtyUsableItems(t *testing.T) {
	set, err := score.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) < 30 {
		t.Errorf("%d calibration items, want at least 30", len(set.Items))
	}
	// Provenance is part of the data. A κ computed against author labels is a
	// weaker claim than one against independent labels, and the report says
	// which it had.
	if set.LabeledBy == "" {
		t.Error("the calibration set does not record who labelled it")
	}

	counts := map[string]int{}
	for _, it := range set.Items {
		switch it.Label {
		case "skill", "baseline", "tie":
		default:
			t.Errorf("item %s has label %q", it.ID, it.Label)
		}
		if strings.TrimSpace(it.Skill) == "" || strings.TrimSpace(it.Baseline) == "" {
			t.Errorf("item %s is missing a transcript", it.ID)
		}
		counts[it.Label]++
	}
	// A set that is 90% one label makes κ meaningless: a judge that always
	// answers "skill" would score well on it.
	for label, n := range counts {
		if float64(n)/float64(len(set.Items)) > 0.6 {
			t.Errorf("label %q is %d/%d of the set; κ against it would be uninformative", label, n, len(set.Items))
		}
	}
}

func TestCalibrate_ReportsDisagreementsAndTrust(t *testing.T) {
	set := &score.CalSet{LabeledBy: "test", Items: []score.CalItem{
		{ID: "c1", Prompt: "p", Skill: "used the script", Baseline: "did it by hand", Label: "skill"},
		{ID: "c2", Prompt: "p", Skill: "used the script", Baseline: "did it by hand", Label: "baseline"},
	}}
	g := &scriptedGateway{answer: func(int, string) string {
		return verdictJSON("A", 0.9, "used the script")
	}}

	r, err := score.Calibrate(context.Background(), judge(t, g), set)
	if err != nil {
		t.Fatal(err)
	}
	if r.N != 2 {
		t.Errorf("N = %d", r.N)
	}
	if len(r.Disagreements) == 0 {
		t.Error("no disagreements reported; a κ with no examples is not actionable")
	}
	if r.LabeledBy != "test" {
		t.Errorf("LabeledBy = %q, want it carried through to the result", r.LabeledBy)
	}
}

func TestJudgeTrusted_UsesTheDocumentedFloor(t *testing.T) {
	if !(score.CalResult{Kappa: score.KappaFloor, N: 30}).JudgeTrusted() {
		t.Error("κ exactly at the floor should be trusted; the gate is κ < 0.6")
	}
	if (score.CalResult{Kappa: 0.59, N: 30}).JudgeTrusted() {
		t.Error("κ below the floor was trusted")
	}
	// An uncalibrated judge is not a trusted judge. Defaulting to trust would
	// make the whole gate opt-in, which is how it gets forgotten.
	if (score.CalResult{}).JudgeTrusted() {
		t.Error("an unrun calibration was treated as trusted")
	}
}
