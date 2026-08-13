package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
)

func member(agent, model, class string) report.PanelMember {
	return report.PanelMember{Agent: agent, Model: model, Class: class}
}

func input(members ...report.MemberInput) report.ComposeInput {
	panel := make([]report.PanelMember, 0, len(members))
	for _, m := range members {
		panel = append(panel, m.Member)
	}
	return report.ComposeInput{
		Skill: "demo", Tier: "full", SuiteRef: "abc", EngineVersion: "test",
		ModelPanel: panel, PanelComplete: true, Members: members,
		StartedAt: time.Unix(0, 0).UTC(), FinishedAt: time.Unix(60, 0).UTC(),
	}
}

// The claim a published score makes is "this works", and it only works if it
// works on the weakest model someone runs it on.
func TestCompose_HeadlineIsTheLowestHealthyMember(t *testing.T) {
	rep, err := report.Compose(input(
		report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 90, Healthy: true},
		report.MemberInput{Member: member("claude-code", "haiku", "floor"), Score: 60, Healthy: true},
	))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if rep.Headline != 60 {
		t.Errorf("Headline = %v, want 60", rep.Headline)
	}
	if rep.SchemaVersion != report.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", rep.SchemaVersion, report.SchemaVersion)
	}
}

// An expired token must not read as a skill that scores zero: that turns
// infrastructure flakiness into a publish block.
func TestCompose_AnUnhealthyMemberIsExcludedNotZeroed(t *testing.T) {
	rep, err := report.Compose(input(
		report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 90, Healthy: true},
		report.MemberInput{Member: member("claude-code", "haiku", "floor"), Healthy: false, Detail: "auth failed"},
	))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if rep.Headline != 90 {
		t.Errorf("Headline = %v, want 90 (the unhealthy member must not drag it to 0)", rep.Headline)
	}
}

func TestCompose_RefusesAPanelWithNoHealthyMember(t *testing.T) {
	_, err := report.Compose(input(
		report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Healthy: false, Detail: "auth failed"},
	))
	if err == nil {
		t.Fatal("Compose produced a score from no measurement")
	}
	if !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("err = %v, want it to name why the member was unhealthy", err)
	}
}

// A zero delta says the skill changed nothing. An absent delta says nobody
// looked. They must not render as the same thing.
func TestCompose_DeltaIsAbsentWhenNoBaselineRan(t *testing.T) {
	in := input(report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 80, Healthy: true})
	rep, err := report.Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if rep.DeltaMeasured {
		t.Error("DeltaMeasured is true with no baseline")
	}
	if rep.Delta != 0 || rep.Baseline != 0 {
		t.Errorf("Delta = %v, Baseline = %v, want both zero and unmeasured", rep.Delta, rep.Baseline)
	}

	in.Baseline, in.BaselineMeasured = 30, true
	rep, err = report.Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !rep.DeltaMeasured || rep.Delta != 50 {
		t.Errorf("Delta = %v (measured=%v), want 50", rep.Delta, rep.DeltaMeasured)
	}
}

func TestCompose_RefusesAScoreOutsideTheScale(t *testing.T) {
	_, err := report.Compose(input(
		report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 140, Healthy: true},
	))
	if err == nil {
		t.Fatal("Compose accepted a score above 100")
	}
}

// A void eval is dropped from the scored set but stays listed: a suite quietly
// losing evals changes what the score means.
func TestCompose_VoidEvalsAreExcludedButListed(t *testing.T) {
	in := input(report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 80, Healthy: true})
	in.Tasks = []report.TaskInput{{TaskID: "1"}, {TaskID: "2"}}
	in.Void = []report.VoidTask{{TaskID: "2", Reason: "no expectations to grade"}}

	rep, err := report.Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(rep.Tasks) != 1 || rep.Tasks[0].TaskID != "1" {
		t.Errorf("Tasks = %+v, want only eval 1", rep.Tasks)
	}
	if len(rep.VoidTasks) != 1 {
		t.Errorf("VoidTasks = %+v, want the excluded eval listed", rep.VoidTasks)
	}
}

func TestReport_RoundTripsThroughJSON(t *testing.T) {
	in := input(report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 80, Healthy: true})
	in.Tasks = []report.TaskInput{{
		TaskID:     "1",
		Conditions: []report.ConditionReport{{Condition: store.Condition("skill"), Model: "sonnet", Passes: 3, Runs: 4}},
		Grades: []report.GradeNote{{
			Model: "sonnet", Condition: store.Condition("skill"), Attempt: 1,
			Expectations: []score.Expectation{{Text: "out.csv exists", Passed: true, Evidence: "step 3"}},
		}},
	}}
	rep, err := report.Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	var buf bytes.Buffer
	if err := rep.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := report.Load(&buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Headline != rep.Headline || len(got.Tasks) != 1 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if len(got.Tasks[0].Grades[0].Expectations) != 1 {
		t.Error("round trip lost the expectation detail")
	}
}

// A field that changed meaning is worse than a field that is missing.
func TestLoad_RefusesANewerSchema(t *testing.T) {
	body := `{"schema_version": 999, "skill": "demo"}`
	if _, err := report.Load(strings.NewReader(body)); err == nil {
		t.Fatal("Load accepted a report from a newer schema")
	}
}

func TestComparable_RefusesEveryFactThatMovesTheNumber(t *testing.T) {
	base := func() *report.Report {
		rep, err := report.Compose(input(
			report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 80, Healthy: true},
		))
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		rep.GraderModel = "claude-sonnet-5"
		return rep
	}

	for _, tc := range []struct {
		name string
		edit func(*report.Report)
		want string
	}{
		{"a different skill", func(r *report.Report) { r.Skill = "other" }, "different skills"},
		{"a different eval set", func(r *report.Report) { r.SuiteRef = "def" }, "different eval sets"},
		{"a different engine", func(r *report.Report) { r.EngineVersion = "next" }, "different engine versions"},
		{"a different tier", func(r *report.Report) { r.Tier = "smoke" }, "different tiers"},
		{"a different grader", func(r *report.Report) { r.GraderModel = "other-model" }, "different grader models"},
		{"a different panel", func(r *report.Report) {
			r.ModelPanel = []report.PanelMember{member("claude-code", "opus", "strong")}
		}, "different model panels"},
		{"a different CLI build", func(r *report.Report) {
			r.ModelPanel = []report.PanelMember{{Agent: "claude-code", Model: "sonnet", Class: "strong", CLIVersion: "9.9"}}
		}, "different agent CLI versions"},
		{"an incomplete panel", func(r *report.Report) { r.PanelComplete = false }, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := base(), base()
			tc.edit(b)
			ok, why := a.Comparable(b)
			if ok {
				t.Fatalf("Comparable accepted %s", tc.name)
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", why, tc.want)
			}
		})
	}

	// The score itself and the spec version must not affect comparability:
	// comparing v3 against v4 is the only question this method exists to answer.
	a, b := base(), base()
	b.Headline, b.SpecVersion = 12, 99
	if ok, why := a.Comparable(b); !ok {
		t.Errorf("Comparable refused two reports that differ only in score and spec version: %s", why)
	}
}

func TestHTML_RendersTheHeadlineAndTheDelta(t *testing.T) {
	in := input(report.MemberInput{Member: member("claude-code", "sonnet", "strong"), Score: 80, Healthy: true})
	in.Baseline, in.BaselineMeasured = 30, true
	in.Tasks = []report.TaskInput{{
		TaskID: "1",
		Grades: []report.GradeNote{{
			Model: "sonnet", Attempt: 1,
			Expectations: []score.Expectation{{Text: "out.csv exists", Passed: false, Evidence: "no such file"}},
		}},
	}}
	rep, err := report.Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	var buf bytes.Buffer
	if err := rep.HTML(&buf); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	page := buf.String()
	for _, want := range []string{"80.0", "50.0", "out.csv exists", "no such file"} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered report does not mention %q", want)
		}
	}
}
