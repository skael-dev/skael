package quality_test

import (
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/spec"
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

// A nil report must be rejected with an error, not dereferenced. Mirrors the
// precedent report.Comparable already sets for a nil report.
func TestFromReport_NilReportReturnsError(t *testing.T) {
	if _, err := quality.FromReport(nil); err == nil {
		t.Fatal("FromReport(nil) did not error")
	}
}

// A memberless, panel-less report must marshal PanelMatrix and ModelPanel as
// JSON arrays, not the null a nil-slice json.Marshal produces — the columns
// are JSONB NOT NULL DEFAULT '[]', array-shaped by contract, and "no data"
// must be distinguishable from "explicitly empty".
func TestFromReport_EmptyMembersAndPanelMarshalAsEmptyArrays(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r"}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec.PanelMatrix) != "[]" {
		t.Fatalf("panel_matrix = %s, want []", rec.PanelMatrix)
	}
	if string(rec.ModelPanel) != "[]" {
		t.Fatalf("model_panel = %s, want []", rec.ModelPanel)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(rec.PanelMatrix, &arr); err != nil {
		t.Fatalf("panel_matrix did not unmarshal as an array: %v", err)
	}
	if err := json.Unmarshal(rec.ModelPanel, &arr); err != nil {
		t.Fatalf("model_panel did not unmarshal as an array: %v", err)
	}
}

func TestFromReport_CountsCriticalForbidViolations(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r"}
	r.Tasks = []report.TaskReport{{
		TaskID: "t1",
		Drift: []report.RunDrift{{
			Agent: "claude-code", Model: "opus", Attempt: 1,
			Violations: []drift.Violation{
				{ID: "no-network", Severity: spec.SeverityCritical, Hits: 2},
				{ID: "no-network", Severity: spec.SeverityCritical, Hits: 1},
				{ID: "tidy", Severity: spec.SeverityMinor, Hits: 5},
			},
		}},
	}}

	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.CriticalForbidViolations != 2 {
		t.Fatalf("critical forbid violations = %d, want 2 — count distinct critical violation records, not hits, and never count non-critical ones", rec.CriticalForbidViolations)
	}
}

func TestFromReport_ZeroCriticalForbidViolationsWhenNoneObserved(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r"}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.CriticalForbidViolations != 0 {
		t.Fatalf("critical forbid violations = %d, want 0 — a report with no violations must count zero, and zero here genuinely means none, unlike an absent measurement, which is what the gate treats a nil QualityState as", rec.CriticalForbidViolations)
	}
}
