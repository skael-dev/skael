package quality_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/quality"
)

func liftReport(schema int, reused []string) *report.Report {
	lead := report.PanelMember{Agent: "claude-code", Model: "lead"}
	floor := report.PanelMember{Agent: "claude-code", Model: "floor"}
	return &report.Report{
		SchemaVersion: schema, Skill: "deploy-helper", SuiteRef: "sha256:abc",
		ModelPanel: []report.PanelMember{lead, floor},
		Members: []report.MemberReport{
			{Member: lead, Healthy: true, Effectiveness: 71},
			{Member: floor, Healthy: true, Effectiveness: 38},
		},
		Headline: 38, Baseline: 44, DeltaMeasured: true,
		// A schema 2 report carries headline minus baseline here. FromReport
		// must not believe it.
		Delta:           float64(38 - 44),
		ReusedBaselines: reused,
	}
}

func TestFromReport_LiftIsRecomputedFromThePrimaryMember(t *testing.T) {
	for _, schema := range []int{2, report.SchemaVersion} {
		rec, err := quality.FromReport(liftReport(schema, nil))
		if err != nil {
			t.Fatalf("schema %d: %v", schema, err)
		}
		if rec.Lift == nil || *rec.Lift != 27 {
			t.Errorf("schema %d: lift = %v, want 27", schema, rec.Lift)
		}
		if rec.PrimaryScore == nil || *rec.PrimaryScore != 71 {
			t.Errorf("schema %d: primary score = %v, want 71", schema, rec.PrimaryScore)
		}
		if rec.Baseline == nil || *rec.Baseline != 44 {
			t.Errorf("schema %d: baseline = %v, want 44", schema, rec.Baseline)
		}
	}
}

func TestFromReport_RecordsHowTheBaselineWasObtained(t *testing.T) {
	fresh, err := quality.FromReport(liftReport(report.SchemaVersion, nil))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.UpliftSource != "fresh" {
		t.Errorf("uplift source = %q, want fresh", fresh.UpliftSource)
	}

	reused, err := quality.FromReport(liftReport(report.SchemaVersion, []string{"task-1"}))
	if err != nil {
		t.Fatal(err)
	}
	if reused.UpliftSource != "reused" {
		t.Errorf("uplift source = %q, want reused", reused.UpliftSource)
	}
}

func TestFromReport_NoBaselineLeavesLiftUnmeasured(t *testing.T) {
	r := liftReport(report.SchemaVersion, nil)
	r.Baseline, r.Delta, r.DeltaMeasured = 0, 0, false

	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Lift != nil || rec.Baseline != nil {
		t.Errorf("lift = %v, baseline = %v, want both nil", rec.Lift, rec.Baseline)
	}
	if rec.UpliftSource != "" {
		t.Errorf("uplift source = %q, want empty", rec.UpliftSource)
	}
	if rec.PrimaryScore == nil || *rec.PrimaryScore != 71 {
		t.Errorf("primary score = %v, want 71 — the member still scored", rec.PrimaryScore)
	}
}
