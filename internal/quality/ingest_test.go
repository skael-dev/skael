package quality_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/quality"
)

func TestFromReport_CarriesTheHeadlineAndPanelState(t *testing.T) {
	gap := 0.18
	r := &report.Report{
		SchemaVersion: report.SchemaVersion, Skill: "deploy-helper", SpecVersion: 4,
		Tier: "full", SuiteRef: "sha256:abc", EngineVersion: "0.9.1",
		Headline: 72.5, HeadlineCI: [2]float64{68, 77}, PanelComplete: true,
		RobustnessGap: &gap,
		ModelPanel:    []report.PanelMember{{Agent: "claude-code", Model: "opus", Class: "strong"}},
		Members:       []report.MemberReport{{Healthy: true, DriftGrade: "B"}},
	}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Headline != 72.5 || rec.HeadlineCILow != 68 || rec.HeadlineCIHigh != 77 {
		t.Fatalf("headline lost: %+v", rec)
	}
	if rec.RobustnessGap == nil || *rec.RobustnessGap != 0.18 {
		t.Fatalf("robustness gap = %v, want 0.18", rec.RobustnessGap)
	}
	if !rec.PanelComplete || rec.SuiteRef != "sha256:abc" || rec.Tier != "full" {
		t.Fatalf("provenance lost: %+v", rec)
	}
}

// An absent measurement is a pointer or an error, never a zero. A nil gap must
// survive as NULL, because 0.0 means "the floor model kept up" — the opposite.
func TestFromReport_AbsentRobustnessGapStaysAbsent(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r", RobustnessGap: nil}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.RobustnessGap != nil {
		t.Fatalf("nil gap became %v", *rec.RobustnessGap)
	}
}

func TestFromReport_RejectsANewerSchema(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion + 1, Skill: "x", SuiteRef: "r"}
	if _, err := quality.FromReport(r); err == nil {
		t.Fatal("a report from a newer schema was ingested")
	}
}

// An incomplete panel is incomplete, never a low score. Ingestion must record
// it as such rather than storing the headline as if the panel were whole.
func TestFromReport_IncompletePanelIsRecordedNotFlattened(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r",
		PanelComplete: false, Headline: 40}
	rec, _ := quality.FromReport(r)
	if rec.PanelComplete {
		t.Fatal("panel_complete was not carried")
	}
	if rec.Headline != 40 {
		t.Fatal("headline was silently rewritten")
	}
}
