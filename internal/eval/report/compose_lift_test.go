package report_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/report"
)

// Subtracting the baseline from the headline minimum instead reports a negative
// lift for a skill that helped.
func TestComposeLiftPairsThePrimaryMemberWithItsOwnBaseline(t *testing.T) {
	lead := report.PanelMember{Agent: "claude-code", Model: "lead"}
	floor := report.PanelMember{Agent: "claude-code", Model: "floor"}

	rep, err := report.Compose(report.ComposeInput{
		Skill:      "demo",
		SuiteRef:   "ref",
		ModelPanel: []report.PanelMember{lead, floor},
		Members: []report.MemberInput{
			{Member: lead, Score: 71, Healthy: true},
			{Member: floor, Score: 38, Healthy: true},
		},
		Baseline:         44,
		BaselineMeasured: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if rep.Headline != 38 {
		t.Errorf("headline = %v, want the panel minimum 38", rep.Headline)
	}
	if !rep.DeltaMeasured || rep.Delta != 27 {
		t.Errorf("lift = %v (measured %v), want the lead's 71 minus its own baseline 44", rep.Delta, rep.DeltaMeasured)
	}
}

func TestComposeReportsNoLiftWhenThePrimaryMemberIsUnhealthy(t *testing.T) {
	lead := report.PanelMember{Agent: "claude-code", Model: "lead"}
	floor := report.PanelMember{Agent: "claude-code", Model: "floor"}

	rep, err := report.Compose(report.ComposeInput{
		Skill:      "demo",
		SuiteRef:   "ref",
		ModelPanel: []report.PanelMember{lead, floor},
		Members: []report.MemberInput{
			{Member: lead, Healthy: false, Detail: "probe failed"},
			{Member: floor, Score: 60, Healthy: true},
		},
		Baseline:         44,
		BaselineMeasured: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if rep.DeltaMeasured {
		t.Errorf("lift measured = true, want false when the primary member did not score")
	}
	if rep.Delta != 0 {
		t.Errorf("lift = %v, want 0 alongside delta_measured false", rep.Delta)
	}
	if rep.Baseline != 44 {
		t.Errorf("baseline = %v, want 44 — it was still measured", rep.Baseline)
	}
}
