package report_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/report"
)

func renderHTML(t *testing.T, r *report.Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.HTML(&buf); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	return buf.String()
}

// externalRef matches any attribute pointing at another host.
var externalRef = regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//`)

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

func TestHTML_RendersWithNothingInIt(t *testing.T) {
	// A smoke run with every session failed still has to produce a readable
	// page: that is exactly when someone opens the report.
	got := renderHTML(t, &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", Tier: "smoke"})
	if !strings.Contains(got, "x") {
		t.Error("an empty report did not render")
	}
}
