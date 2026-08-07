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
		Skill: "csv-to-markdown", Tier: "smoke", Headline: 0, PanelComplete: true,
		Members: []report.MemberReport{{
			Member:  report.PanelMember{Agent: "claude-code", Model: "opus", Class: "strong"},
			Pillars: score.Pillars{TriggerF1: 1, Reliability: 0, Uplift: 0.5, Efficiency: 1},
			Healthy: true,
		}},
		Tasks: []report.TaskReport{{
			TaskID: "edge-ragged-rows-pad", Kind: "edge", Split: "dev",
			Conditions: []report.ConditionReport{{
				Condition: "skill", Model: "opus", Passes: 0, Runs: 1,
				Reason: "ragged_rows entries need line and field_count",
			}},
		}},
	}
}

func TestRenderEvalSummary_NamesEachFailingTaskAndItsReason(t *testing.T) {
	got := whetstone.RenderEvalSummary(failingReport(), 5, "csv-to-markdown", false)

	for _, want := range []string{
		"edge-ragged-rows-pad",
		"ragged_rows entries need line and field_count",
		"0 of 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary does not contain %q:\n%s", want, got)
		}
	}
}

// Uplift at a tier with no baseline is 0.5 by construction. Printing it as a
// number invites the reader to interpret a measurement that was never taken.
func TestRenderEvalSummary_SaysNotMeasuredWithoutABaseline(t *testing.T) {
	got := whetstone.RenderEvalSummary(failingReport(), 5, "csv-to-markdown", false)
	if !strings.Contains(got, "not measured") {
		t.Errorf("summary does not mark uplift as unmeasured:\n%s", got)
	}
	if strings.Contains(got, "0.50") || strings.Contains(got, "0.5 ") {
		t.Errorf("summary printed the placeholder uplift value:\n%s", got)
	}
}

func TestRenderEvalSummary_ShowsUpliftWhenABaselineRan(t *testing.T) {
	rep := failingReport()
	rep.Members[0].Pillars.Uplift = 0.72
	got := whetstone.RenderEvalSummary(rep, 5, "csv-to-markdown", true)
	if !strings.Contains(got, "0.72") {
		t.Errorf("summary omitted a measured uplift:\n%s", got)
	}
	if strings.Contains(got, "not measured") {
		t.Errorf("summary called a measured uplift unmeasured:\n%s", got)
	}
}

func TestRenderEvalSummary_PassingRunListsNoFailures(t *testing.T) {
	rep := failingReport()
	rep.Headline = 91
	rep.Tasks[0].Conditions[0].Passes = 1
	rep.Tasks[0].Conditions[0].Reason = ""
	rep.Members[0].Pillars.Reliability = 1

	got := whetstone.RenderEvalSummary(rep, 5, "csv-to-markdown", false)
	if strings.Contains(got, "edge-ragged-rows-pad") {
		t.Errorf("a passing task was listed as a failure:\n%s", got)
	}
	if !strings.Contains(got, "1 of 1") {
		t.Errorf("summary does not report the pass count:\n%s", got)
	}
}
