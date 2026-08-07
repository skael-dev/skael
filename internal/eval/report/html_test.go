package report_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/score"
)

func renderHTML(t *testing.T, r *report.Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.HTML(&buf); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	return buf.String()
}

// externalRef matches any attribute or CSS url()/@import pointing at another
// host, whether as an HTML attribute (src=/href=) or inside the inline
// stylesheet (@import url(...), background: url(https://...)).
var externalRef = regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//|url\(\s*["']?(https?:)?//|@import\s+["']?(https?:)?//`)

func TestHTML_IsSelfContained(t *testing.T) {
	got := renderHTML(t, demoReport())
	// A report that fetches anything is a report that stops rendering offline
	// and tells a third party which skills a team evaluated.
	if m := externalRef.FindString(got); m != "" {
		t.Errorf("report references an external resource: %q", m)
	}
	if !strings.Contains(got, "<style") {
		t.Error("no inline stylesheet; the report depends on the browser's defaults")
	}
}

func TestHTML_EscapesModelAuthoredText(t *testing.T) {
	r := demoReport()
	r.Tasks = []report.TaskReport{{
		TaskID: "t1",
		Judge: []report.JudgeNote{{
			Model: "opus", Winner: "skill", Margin: 0.6,
			Evidence: []string{`<script>alert("x")</script> and a "quoted" phrase`},
		}},
	}}
	got := renderHTML(t, r)
	// Task prompts, judge quotes, and violation evidence are all model-authored
	// or agent-authored text rendered into a page a human opens. html/template
	// escapes by default; this is the test that fails if someone reaches for
	// template.HTML to fix a formatting annoyance.
	if strings.Contains(got, "<script>alert") {
		t.Error("judge evidence was rendered unescaped")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("the evidence text is missing entirely; it should be escaped, not dropped")
	}
}

func TestHTML_ShowsEveryPanelMemberIncludingUnhealthyOnes(t *testing.T) {
	r := demoReport()
	r.PanelComplete = false
	r.Members = []report.MemberReport{
		{Member: r.ModelPanel[0], Effectiveness: 82, Healthy: true, DriftGrade: "B"},
		{Member: r.ModelPanel[1], Healthy: false, Detail: "auth expired"},
	}
	got := renderHTML(t, r)
	for _, want := range []string{"opus", "haiku", "auth expired", "incomplete"} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q", want)
		}
	}
	// An unhealthy member rendered as a blank row reads as a zero. Saying why is
	// the difference between "this skill fails on the floor model" and "we could
	// not ask the floor model".
}

func TestHTML_StatesTheUpliftSourceAndKappa(t *testing.T) {
	r := demoReport()
	r.UpliftSource = "passrate-fallback"
	k := 0.41
	r.JudgeKappa = &k
	got := renderHTML(t, r)
	for _, want := range []string{"passrate-fallback", "0.41"} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not surface %q; a demoted judge must be visible on the score it produced", want)
		}
	}
}

func TestHTML_SurfacesTriggerUnknownAndMetaPartial(t *testing.T) {
	r := demoReport()
	r.TriggerUnknown = 2
	r.Members = []report.MemberReport{{
		Member:            r.ModelPanel[0],
		Healthy:           true,
		MetaPartial:       true,
		MetaPartialReason: "resumed run: recovering full meta failed",
	}}
	got := renderHTML(t, r)
	for _, want := range []string{"2 trigger probe", "partial meta"} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q", want)
		}
	}
}

func TestHTML_SurfacesTriggerSource(t *testing.T) {
	r := demoReport()
	r.TriggerSource = report.PanelMember{Agent: "claude-code", Model: "haiku", Class: "floor"}
	got := renderHTML(t, r)
	// Trigger F1 is measured once, on a single panel member, and copied into
	// every member's row — a reader must be able to see which member the
	// figure actually came from rather than assume it was measured per row.
	if !strings.Contains(got, "claude-code/haiku") {
		t.Errorf("report does not name the trigger source member (claude-code/haiku): %s", got)
	}
}

func TestHTML_SurfacesUnevaluableCountPerDriftRun(t *testing.T) {
	r := demoReport()
	r.Tasks = []report.TaskReport{{
		TaskID: "t1",
		Drift: []report.RunDrift{{
			Model:   "opus",
			Attempt: 1,
			Result:  drift.Result{Adherence: 80, Unevaluable: 3},
		}},
	}}
	got := renderHTML(t, r)
	if !strings.Contains(got, "Unevaluable") {
		t.Error("per-run drift table does not have an Unevaluable column")
	}
	if !strings.Contains(got, ">3<") {
		t.Errorf("report does not show the run's unevaluable count of 3; got:\n%s", got)
	}
}

func TestHTML_SurfacesVoidTasksAndUnevaluableChecks(t *testing.T) {
	r := demoReport()
	r.VoidTasks = []report.VoidTask{{TaskID: "t07", Reason: "the oracle failed"}}
	r.Unevaluable = 2
	r.UnevaluableDetail = []string{"/etc/passwd against out/**"}
	got := renderHTML(t, r)
	for _, want := range []string{"t07", "the oracle failed", "/etc/passwd"} {
		if !strings.Contains(got, want) {
			t.Errorf("report hides %q; the reader cannot tell what was not measured", want)
		}
	}
}

func TestHTML_RendersDriftMeanAndWorstAsAdherenceNotAHundredfoldPercentage(t *testing.T) {
	// Agg.Mean/Worst/Sigma are already on a 0-100 adherence scale (means of
	// Adherence), not a [0,1] rate. The html_test.go:64 fixture leaves Drift
	// zero and so cannot catch a template that renders it through pct — this
	// fixture is deliberately non-zero.
	r := demoReport()
	r.Members = []report.MemberReport{{
		Member:  r.ModelPanel[0],
		Healthy: true,
		Drift:   drift.Agg{Mean: 87.5, Worst: 62.3, Sigma: 4.1, N: 2},
	}}
	got := renderHTML(t, r)
	if !strings.Contains(got, "87.5") {
		t.Errorf("report does not show the drift mean as 87.5; got:\n%s", got)
	}
	if !strings.Contains(got, "62.3") {
		t.Errorf("report does not show the drift worst as 62.3; got:\n%s", got)
	}
	if strings.Contains(got, "8750.0%") || strings.Contains(got, "6230.0%") {
		t.Error("drift mean/worst rendered as a hundredfold percentage (fed through pct instead of round1)")
	}
}

func TestHTML_PctRefusesAnOutOfRangeValueRatherThanMisrenderingIt(t *testing.T) {
	// A raw *Report bypasses Compose's validation, so a caller that builds one
	// by hand (as this test does) can still hand the template a pillar above
	// 1. pct must refuse it visibly rather than print a nonsense percentage or
	// panic and blank the whole report.
	r := demoReport()
	r.Members = []report.MemberReport{{
		Member:  r.ModelPanel[0],
		Healthy: true,
		Pillars: score.Pillars{TriggerF1: 87.5, Reliability: 0.9, Uplift: 0.9, Efficiency: 0.9},
	}}
	got := renderHTML(t, r)
	if !strings.Contains(got, "invalid pct input") {
		t.Errorf("report did not surface pct's refusal of an out-of-range input; got:\n%s", got)
	}
}

func TestHTML_RendersWithNothingInIt(t *testing.T) {
	// A smoke run with every session failed still has to produce a readable
	// page: that is exactly when someone opens the report.
	got := renderHTML(t, &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", Tier: "smoke"})
	if !strings.Contains(got, "x") {
		t.Error("an empty report did not render")
	}
}

func TestHTML_ShowsTheVerifiersReason(t *testing.T) {
	rep := report.Report{
		Skill: "csv-to-markdown", Tier: "smoke",
		Tasks: []report.TaskReport{{
			TaskID: "edge-ragged-rows-pad", Kind: "edge", Split: "dev",
			Conditions: []report.ConditionReport{{
				Condition: "skill", Model: "opus", Passes: 0, Runs: 1,
				Reason: "ragged_rows entries need line and field_count",
			}},
		}},
	}

	var buf bytes.Buffer
	if err := rep.HTML(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ragged_rows entries need line and field_count") {
		t.Error("the report does not show the verifier's reason")
	}
}

// The reason is model-authored text. html/template escapes by default; this
// pins that the template never marks it safe.
func TestHTML_EscapesTheReason(t *testing.T) {
	rep := report.Report{
		Skill: "s", Tier: "smoke",
		Tasks: []report.TaskReport{{
			TaskID: "t1",
			Conditions: []report.ConditionReport{{
				Condition: "skill", Model: "opus", Passes: 0, Runs: 1,
				Reason: `<script>alert(1)</script>`,
			}},
		}},
	}

	var buf bytes.Buffer
	if err := rep.HTML(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<script>alert(1)</script>") {
		t.Error("the reason was not escaped")
	}
	if !strings.Contains(buf.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the reason was not escaped")
	}
}
