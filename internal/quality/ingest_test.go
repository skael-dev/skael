package quality_test

import (
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/quality"
)

func TestFromReport_CarriesTheHeadlineAndPanelState(t *testing.T) {
	r := &report.Report{
		SchemaVersion: report.SchemaVersion, Skill: "deploy-helper", SpecVersion: 4,
		Tier: "full", SuiteRef: "sha256:abc", EngineVersion: "0.9.1",
		Headline: 72.5, PanelComplete: true,
		ModelPanel: []report.PanelMember{{Agent: "claude-code", Model: "sonnet", Class: "strong"}},
		Members:    []report.MemberReport{{Healthy: true, Effectiveness: 72.5}},
	}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Headline != 72.5 {
		t.Fatalf("headline lost: %+v", rec)
	}
	if !rec.PanelComplete || rec.SuiteRef != "sha256:abc" || rec.Tier != "full" {
		t.Fatalf("provenance lost: %+v", rec)
	}
}

func TestFromReport_CarriesTheGraderModel(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r", GraderModel: "claude-opus-5"}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.JudgeModel == nil || *rec.JudgeModel != "claude-opus-5" {
		t.Fatalf("judge model = %v, want claude-opus-5", rec.JudgeModel)
	}
}

// A report with no judge (JudgeModel == "") must record nil, not an empty
// string — the empty string is not a distinct fact from "we don't know",
// while nil is, and the series grouping in internal/quality/series.go relies
// on being able to tell them apart.
func TestFromReport_NoJudgeModelStaysNil(t *testing.T) {
	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r"}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if rec.JudgeModel != nil {
		t.Fatalf("judge model = %v, want nil", *rec.JudgeModel)
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
