package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/score"
)

func demoReport() *report.Report {
	k := 0.71
	gap := 18.2
	return &report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         "csv-to-md",
		SpecVersion:   3,
		Tier:          "full",
		SuiteRef:      "aaaaaaaaaaaa1111",
		EngineVersion: "0.9.0",
		ModelPanel: []report.PanelMember{
			{Agent: "claude-code", Model: "opus", Class: "strong", CLIVersion: "2.1.220"},
			{Agent: "claude-code", Model: "haiku", Class: "floor", CLIVersion: "2.1.220"},
		},
		PanelComplete:  true,
		Headline:       61.4,
		HeadlineCI:     [2]float64{57.1, 65.9},
		UpliftSource:   score.UpliftJudge,
		JudgeKappa:     &k,
		JudgeLabeledBy: "author",
		RobustnessGap:  &gap,
		StartedAt:      time.Unix(1700000000, 0).UTC(),
		FinishedAt:     time.Unix(1700004000, 0).UTC(),
	}
}

func demoComposeInput() report.ComposeInput {
	panel := []report.PanelMember{
		{Agent: "claude-code", Model: "opus", Class: "strong", CLIVersion: "2.1.220"},
		{Agent: "claude-code", Model: "haiku", Class: "floor", CLIVersion: "2.1.220"},
	}
	return report.ComposeInput{
		Skill:         "csv-to-md",
		SpecVersion:   3,
		Tier:          "full",
		SuiteRef:      "aaaaaaaaaaaa1111",
		EngineVersion: "0.9.0",
		ModelPanel:    panel,
		PanelComplete: true,
		Members: []report.MemberInput{
			{
				Member:  panel[0],
				Pillars: score.Pillars{TriggerF1: 0.9, Reliability: 0.85, Uplift: 0.7, Efficiency: 0.9},
				Healthy: true,
				Drift: []drift.Result{
					{Components: drift.Components{StepCoverage: 1, Order: 1, Violation: 1, Checkpoint: 1, Semantic: 0.9, Focus: 1}, Adherence: 92},
					{Components: drift.Components{StepCoverage: 1, Order: 1, Violation: 1, Checkpoint: 1, Semantic: 0.85, Focus: 1}, Adherence: 90},
				},
			},
			{
				Member:  panel[1],
				Pillars: score.Pillars{TriggerF1: 0.8, Reliability: 0.7, Uplift: 0.6, Efficiency: 0.85},
				Healthy: true,
				Drift: []drift.Result{
					{Components: drift.Components{StepCoverage: 0.8, Order: 0.9, Violation: 1, Checkpoint: 1, Semantic: 0.6, Focus: 1}, Adherence: 78},
					{Components: drift.Components{StepCoverage: 0.75, Order: 0.9, Violation: 1, Checkpoint: 1, Semantic: 0.6, Focus: 1}, Adherence: 75},
				},
			},
		},
		Tasks: []report.TaskInput{
			{
				TaskID: "t01",
				Kind:   "generate",
				Split:  "dev",
				Conditions: []report.ConditionReport{
					{Condition: "skill", Model: "opus", Passes: 2, Runs: 2},
					{Condition: "baseline", Model: "opus", Passes: 1, Runs: 2},
				},
			},
			{
				TaskID: "t02",
				Kind:   "generate",
				Split:  "dev",
				Conditions: []report.ConditionReport{
					{Condition: "skill", Model: "haiku", Passes: 1, Runs: 2},
					{Condition: "baseline", Model: "haiku", Passes: 0, Runs: 2},
				},
			},
			{
				TaskID: "t03",
				Kind:   "generate",
				Split:  "dev",
				Conditions: []report.ConditionReport{
					{Condition: "skill", Model: "opus", Passes: 2, Runs: 2},
				},
			},
		},
		JudgeTrusted:   true,
		JudgeKappa:     0.75,
		JudgeLabeledBy: "author",
		StartedAt:      time.Unix(1700000000, 0).UTC(),
		FinishedAt:     time.Unix(1700004000, 0).UTC(),
	}
}

func TestReport_RoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := demoReport().Save(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := report.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Headline != 61.4 || got.SuiteRef != "aaaaaaaaaaaa1111" || got.UpliftSource != score.UpliftJudge {
		t.Errorf("round trip lost data: %+v", got)
	}
	if got.JudgeKappa == nil || *got.JudgeKappa != 0.71 {
		t.Errorf("κ = %v; a pointer distinguishes \"not calibrated\" from \"calibrated at zero\"", got.JudgeKappa)
	}
}

func TestLoad_RejectsAnUnknownSchemaVersion(t *testing.T) {
	_, err := report.Load(strings.NewReader(`{"schema_version":99,"skill":"x"}`))
	// A newer report read by an older binary would be silently misinterpreted:
	// a field that changed meaning is worse than a field that is missing.
	if err == nil {
		t.Error("Load accepted a report from a newer schema")
	}
}

func TestComparable_RequiresTheSameSuiteAndPanel(t *testing.T) {
	a, b := demoReport(), demoReport()
	if ok, why := a.Comparable(b); !ok {
		t.Errorf("identical reports not comparable: %s", why)
	}

	// The whole reason suite_ref is on the report. Two scores measured against
	// different tasks are two different measurements, and a trend that mixes
	// them silently is worse than no trend.
	b = demoReport()
	b.SuiteRef = "bbbbbbbbbbbb2222"
	ok, why := a.Comparable(b)
	if ok {
		t.Error("reports with different suites reported as comparable")
	}
	if !strings.Contains(why, "suite") {
		t.Errorf("reason = %q, want it to name the suite", why)
	}

	// Same for the panel: "the score dropped" means nothing if the models
	// changed underneath it — which is precisely the regression signal this is
	// supposed to make detectable.
	b = demoReport()
	b.ModelPanel[1].Model = "sonnet"
	ok, why = a.Comparable(b)
	if ok {
		t.Error("reports with different panels reported as comparable")
	}
	if !strings.Contains(why, "panel") {
		t.Errorf("reason = %q, want it to name the panel", why)
	}

	b = demoReport()
	b.Tier = "smoke"
	if ok, _ := a.Comparable(b); ok {
		t.Error("a smoke report was reported as comparable to a full one")
	}

	// An incomplete panel is not comparable to a complete one even when the
	// members match: the min was taken over fewer members.
	b = demoReport()
	b.PanelComplete = false
	if ok, _ := a.Comparable(b); ok {
		t.Error("an incomplete panel was reported as comparable")
	}
}

func TestComparable_IgnoresWhatDoesNotAffectTheMeasurement(t *testing.T) {
	a, b := demoReport(), demoReport()
	b.SpecVersion = 4
	b.Headline = 74.0
	b.StartedAt = a.StartedAt.Add(time.Hour)
	// Comparing v3 against v4 is the entire use case. A different spec version
	// and a different score must not make two reports incomparable, or the
	// method answers no to the only question it is asked.
	if ok, why := a.Comparable(b); !ok {
		t.Errorf("two versions of the same skill on the same suite not comparable: %s", why)
	}
}

func TestReport_CarriesEveryUnevaluableCheckToTheSurface(t *testing.T) {
	r := demoReport()
	r.Unevaluable = 3
	r.UnevaluableDetail = []string{"/etc/passwd against out/**: absolute candidate"}
	var buf bytes.Buffer
	if err := r.Save(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := report.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	// A check that could not be performed is a hole in the measurement. If it
	// stops at the drift package the report reads as clean, and a missed
	// violation is the failure direction nobody investigates.
	if got.Unevaluable != 3 || len(got.UnevaluableDetail) != 1 {
		t.Errorf("unevaluable checks did not survive to the report: %+v", got)
	}
}

func TestCompose_MarksTheFallbackWhenTheJudgeIsUntrusted(t *testing.T) {
	in := demoComposeInput()
	in.JudgeTrusted = false
	in.JudgeKappa = 0.41
	r, err := report.Compose(in)
	if err != nil {
		t.Fatal(err)
	}
	if r.UpliftSource != score.UpliftPassRate {
		t.Errorf("UpliftSource = %q, want the fallback recorded", r.UpliftSource)
	}
	if r.JudgeKappa == nil || *r.JudgeKappa != 0.41 {
		t.Error("the κ that caused the demotion is not on the report")
	}
}

func TestComparable_DifferentUpliftSources(t *testing.T) {
	a, b := demoReport(), demoReport()
	a.UpliftSource = score.UpliftJudge
	b.UpliftSource = score.UpliftPassRate
	// A judge-scored uplift and a pass-rate-derived one are different
	// measurements on the same axis. Charting them as one series is exactly
	// the silent mixing Comparable exists to prevent.
	ok, why := a.Comparable(b)
	if ok {
		t.Error("reports with different uplift sources reported as comparable")
	}
	if !strings.Contains(why, string(score.UpliftJudge)) || !strings.Contains(why, string(score.UpliftPassRate)) {
		t.Errorf("reason = %q, want it to name both uplift sources", why)
	}
}

func TestComparable_PanelOrderDoesNotMatter(t *testing.T) {
	a, b := demoReport(), demoReport()
	b.ModelPanel = []report.PanelMember{a.ModelPanel[1], a.ModelPanel[0]}
	// The planner's iteration order is an implementation detail, not part of
	// what the panel measured. A reordered panel is the same panel.
	if ok, why := a.Comparable(b); !ok {
		t.Errorf("a reordered panel was reported as a different one: %s", why)
	}
}

func TestCompose_LeavesRobustnessGapAbsentWhenTheClassIsAmbiguous(t *testing.T) {
	in := demoComposeInput()
	// A second "strong" member makes ByClass(strong) ambiguous: "the strong
	// member" is no longer defined, so neither is a strong/floor comparison.
	extra := in.Members[0]
	extra.Member = report.PanelMember{Agent: "claude-code", Model: "sonnet", Class: "strong", CLIVersion: "2.1.220"}
	in.Members = append(in.Members, extra)
	in.ModelPanel = append(in.ModelPanel, extra.Member)

	r, err := report.Compose(in)
	if err != nil {
		t.Fatal(err)
	}
	// A zero gap means "the floor model kept up." An ambiguous class means "we
	// could not tell" — the two must not be indistinguishable on the report, and
	// a pointer makes "has a value" and "the value" the same fact so the wrong
	// state cannot even be constructed.
	if r.RobustnessGap != nil {
		t.Errorf("RobustnessGap = %v, want nil with an ambiguous strong class", *r.RobustnessGap)
	}

	var buf bytes.Buffer
	if err := r.Save(&buf); err != nil {
		t.Fatal(err)
	}
	t.Logf("emitted JSON for the absent-gap case:\n%s", buf.String())
	if strings.Contains(buf.String(), "robustness_gap") {
		t.Errorf("serialised report carries a robustness_gap key when the gap is absent: %s", buf.String())
	}

	got, err := report.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.RobustnessGap != nil {
		t.Error("round trip turned an absent gap present")
	}
}

func TestCompose_SetsRobustnessGapWhenTheClassesAreUnambiguous(t *testing.T) {
	in := demoComposeInput()
	r, err := report.Compose(in)
	if err != nil {
		t.Fatal(err)
	}
	// The counterpart to the ambiguous case: with exactly one strong and one
	// floor member, the gap is computable and must appear on the report.
	if r.RobustnessGap == nil {
		t.Fatal("RobustnessGap = nil, want a computed value with unambiguous classes")
	}

	var buf bytes.Buffer
	if err := r.Save(&buf); err != nil {
		t.Fatal(err)
	}
	t.Logf("emitted JSON for the present-gap case:\n%s", buf.String())
	if !strings.Contains(buf.String(), `"robustness_gap":`) {
		t.Errorf("serialised report does not carry robustness_gap when it was computed: %s", buf.String())
	}

	got, err := report.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.RobustnessGap == nil || *got.RobustnessGap != *r.RobustnessGap {
		t.Errorf("round trip lost the robustness gap: got %v, want %v", got.RobustnessGap, r.RobustnessGap)
	}
}

func TestCompose_ExcludesVoidTasksFromScoringAndListsThem(t *testing.T) {
	in := demoComposeInput()
	in.Void = []report.VoidTask{{TaskID: "t03", Reason: "the verifier passes without the oracle having run"}}
	r, err := report.Compose(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range r.Tasks {
		if task.TaskID == "t03" {
			t.Error("a void task was scored")
		}
	}
	// Listed, not merely dropped: a suite quietly losing three of ten tasks
	// changes what the score means and the reader has to be able to see it.
	if len(r.VoidTasks) != 1 {
		t.Errorf("VoidTasks = %+v", r.VoidTasks)
	}
}
