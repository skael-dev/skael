package whetstone

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// TestRenderDrift_MatchesTaskDriftToItsOwnPanelMember covers a cross-vendor
// panel where two members share a model string on different agents (e.g.
// --agents claude-code,cursor --models opus). renderDrift used to associate
// a task's drift rows to a member by Model alone; with two members on the
// same model, that would silently attribute one member's violations to the
// other member's table section — a bug invisible to any test that only ever
// exercises one model.
func TestRenderDrift_MatchesTaskDriftToItsOwnPanelMember(t *testing.T) {
	rep := &report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         "demo",
		Tier:          "smoke",
		SuiteRef:      "deadbeef",
		Members: []report.MemberReport{
			{
				Member:     report.PanelMember{Agent: "claude-code", Model: "opus", Class: string(spec.TierStrong)},
				Healthy:    true,
				Drift:      drift.Agg{Mean: 91.5, Worst: 80, N: 1},
				DriftGrade: "A",
			},
			{
				Member:     report.PanelMember{Agent: "cursor", Model: "opus", Class: string(spec.TierStrong)},
				Healthy:    true,
				Drift:      drift.Agg{Mean: 42.5, Worst: 30, N: 1},
				DriftGrade: "D",
			},
		},
		Tasks: []report.TaskReport{
			{
				TaskID: "t01",
				Drift: []report.RunDrift{
					{
						Agent: "claude-code", Model: "opus", Attempt: 0,
						Result: drift.Result{
							Adherence: 91.5,
							Components: drift.Components{
								StepCoverage: 1, Order: 1, Violation: 1, Checkpoint: 1, Semantic: 1, Focus: 0.5,
							},
						},
						Violations: []drift.Violation{
							{ID: "claude-only-violation", Severity: spec.SeverityMinor, Hits: 1, Evidence: []string{"claude evidence"}},
						},
					},
					{
						Agent: "cursor", Model: "opus", Attempt: 0,
						Result: drift.Result{
							Adherence: 42.5,
							Components: drift.Components{
								StepCoverage: 0.5, Order: 0.5, Violation: 0.2, Checkpoint: 0.5, Semantic: 0.5, Focus: 0.2,
							},
						},
						Violations: []drift.Violation{
							{ID: "cursor-only-violation", Severity: spec.SeverityCritical, Hits: 3, Evidence: []string{"cursor evidence"}},
						},
					},
				},
			},
		},
	}

	out := captureStderr(t, func() { renderDrift(rep) })

	claudeSection, cursorSection := memberSections(out, "claude-code/opus", "cursor/opus")

	// Each member's adherence figure and components show up under its own
	// section, and nowhere else — a genuinely useless render (an empty table,
	// or one member's row printed under both headings) would fail this.
	if !strings.Contains(claudeSection, "91.5") || !strings.Contains(claudeSection, "coverage=1.00") {
		t.Errorf("claude-code section is missing its own adherence/components:\n%s", claudeSection)
	}
	if !strings.Contains(cursorSection, "42.5") || !strings.Contains(cursorSection, "coverage=0.50") {
		t.Errorf("cursor section is missing its own adherence/components:\n%s", cursorSection)
	}

	// The regression itself: each member's violation (and its evidence) must
	// appear in its own section and must not leak into the other's.
	if !strings.Contains(claudeSection, "claude-only-violation") || !strings.Contains(claudeSection, "claude evidence") {
		t.Errorf("claude-code section is missing its own violation:\n%s", claudeSection)
	}
	if strings.Contains(claudeSection, "cursor-only-violation") {
		t.Errorf("claude-code section leaked cursor's violation:\n%s", claudeSection)
	}
	if !strings.Contains(cursorSection, "cursor-only-violation") || !strings.Contains(cursorSection, "cursor evidence") {
		t.Errorf("cursor section is missing its own violation:\n%s", cursorSection)
	}
	if strings.Contains(cursorSection, "claude-only-violation") {
		t.Errorf("cursor section leaked claude-code's violation:\n%s", cursorSection)
	}
}

// captureStderr redirects os.Stderr for the duration of fn (ui.Info/ui.Warn,
// which renderDrift uses, write there) and returns everything written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe writer: %v", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	return string(b)
}

// memberSections splits out into everything printed from each of the two
// given member headings up to the next one (or end of output), so a test
// can assert what did and did not appear under each member's own section.
func memberSections(out, headingA, headingB string) (a, b string) {
	idxA := strings.Index(out, headingA)
	idxB := strings.Index(out, headingB)
	if idxA < 0 || idxB < 0 {
		return "", ""
	}
	if idxA < idxB {
		return out[idxA:idxB], out[idxB:]
	}
	return out[idxA:], out[idxB:idxA]
}
