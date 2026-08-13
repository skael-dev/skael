package whetstone_test

import (
	"strings"
	"testing"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/score"
)

func failingReport() *report.Report {
	return &report.Report{
		Skill: "csv-to-markdown", Tier: "smoke", Headline: 50, PanelComplete: true,
		TriggerF1: 1,
		Members: []report.MemberReport{{
			Member:        report.PanelMember{Agent: "claude-code", Model: "sonnet", Class: "strong"},
			Effectiveness: 50,
			Healthy:       true,
		}},
		Tasks: []report.TaskReport{{
			TaskID: "3",
			Conditions: []report.ConditionReport{
				{Condition: "skill", Model: "sonnet", Passes: 1, Runs: 2},
			},
			Grades: []report.GradeNote{{
				Model: "sonnet", Condition: "skill", Attempt: 1,
				Expectations: []score.Expectation{
					{Text: "the table has a header row", Passed: true, Evidence: "row 1 is a header"},
					{Text: "ragged rows are padded", Passed: false, Evidence: "row 4 has 2 fields, want 3"},
				},
			}},
		}},
	}
}

// A score with no reason attached is the defect this output exists to
// prevent: the reader needs to know which expectation failed.
func TestRenderEvalSummary_NamesEachFailedExpectation(t *testing.T) {
	got := whetstone.RenderEvalSummary(failingReport(), 5, "csv-to-markdown")

	for _, want := range []string{"eval 3", "ragged rows are padded", "1 of 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary does not mention %q:\n%s", want, got)
		}
	}
	// A passing expectation is not a failure and must not be listed as one.
	if strings.Contains(got, "the table has a header row") {
		t.Errorf("summary lists a passing expectation as a failure:\n%s", got)
	}
}

// A baseline that never ran must read as unmeasured, not as a zero delta.
func TestRenderEvalSummary_SaysWhenTheDeltaWasNotMeasured(t *testing.T) {
	got := whetstone.RenderEvalSummary(failingReport(), 5, "csv-to-markdown")
	if !strings.Contains(got, "not measured") {
		t.Errorf("summary does not say the delta was not measured:\n%s", got)
	}

	rep := failingReport()
	rep.Baseline, rep.Delta, rep.DeltaMeasured = 20, 30, true
	got = whetstone.RenderEvalSummary(rep, 5, "csv-to-markdown")
	if !strings.Contains(got, "+30") {
		t.Errorf("summary does not report the measured delta:\n%s", got)
	}
}
