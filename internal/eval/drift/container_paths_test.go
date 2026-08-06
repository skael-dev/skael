package drift_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// This is the regression test for the bug the corpus could not see.
//
// The corpus fixtures under internal/eval/testdata/corpus are hand-authored
// with workspace-relative paths ("out/report.md"), so every scoring test
// passed against data that does not resemble a real agent stream. A real
// claude-code stream reports absolute *container* paths
// ("/workspace/out/report.md"), which contract.MatchPath rejects outright —
// so in production every path-bearing rule was unevaluable while the tests
// stayed green.
//
// What made that worse than a dropped signal is the direction it broke in.
// Step coverage, checkpoints and focus collapsed to zero because nothing
// matched; violation and order went vacuously *perfect* because nothing could
// be violated or mis-ordered either. Adherence became the constant
// 100*(0.15+0.25) = 40, below the 60 a C requires, so every skill graded D
// while "Violation 100%" read as flawless compliance.
func TestObserve_ContainerPathsAreScoredAfterRelativisation(t *testing.T) {
	c := &contract.Contract{Version: 1,
		Steps: []contract.StepMatch{{
			ID:       "s1",
			Match:    contract.Matcher{Type: trajectory.TypeFileWrite, PathGlob: "out/**"},
			Order:    contract.Order{Mode: "any"},
			Required: true,
		}},
		Forbid: []contract.ForbidMatch{{
			ID:       "f1",
			Match:    contract.Matcher{Type: trajectory.TypeFileWrite, PathNotGlob: "out/**"},
			Severity: spec.SeverityCritical,
		}},
	}

	// Exactly what the CLI reports: absolute, rooted at the sandbox workdir.
	raw := []trajectory.Event{
		ev(1, trajectory.TypeFileWrite, "Write", "/workspace/out/jira-issues.json"),
	}

	t.Run("unrelativised, nothing can be evaluated", func(t *testing.T) {
		o, err := drift.Observe(c, raw)
		if err != nil {
			t.Fatal(err)
		}
		if o.Unevaluable == 0 {
			t.Fatal("expected absolute container paths to be unevaluable; " +
				"this test no longer reproduces the original bug")
		}
		if o.Steps[0].Matched {
			t.Error("an unevaluable candidate should not match")
		}
	})

	t.Run("relativised, the step matches and nothing is unevaluable", func(t *testing.T) {
		o, err := drift.Observe(c, trajectory.Relativize(raw, "/workspace"))
		if err != nil {
			t.Fatal(err)
		}
		if o.Unevaluable != 0 {
			t.Errorf("Unevaluable = %d, want 0: %v", o.Unevaluable, o.UnevaluableDetail)
		}
		if !o.Steps[0].Matched {
			t.Error("the write under out/** did not match after relativisation, so step " +
				"coverage would still be 0 and adherence still a constant")
		}
		// The write is inside out/, so the forbid rule must NOT fire. Before
		// relativisation it could not fire either — but for the wrong reason,
		// which is what made a missed violation look like a clean run.
		if len(o.Violations) != 0 {
			t.Errorf("violations = %+v, want none", o.Violations)
		}
	})
}

// The relativiser must not launder a genuine escape into a match. A write
// outside the workspace stays absolute, stays unevaluable, and stays visible.
func TestObserve_APathOutsideTheWorkspaceStaysUnevaluable(t *testing.T) {
	c := &contract.Contract{Version: 1, Steps: []contract.StepMatch{{
		ID:       "s1",
		Match:    contract.Matcher{Type: trajectory.TypeFileWrite, PathGlob: "out/**"},
		Order:    contract.Order{Mode: "any"},
		Required: true,
	}}}

	raw := []trajectory.Event{ev(1, trajectory.TypeFileWrite, "Write", "/etc/passwd")}
	o, err := drift.Observe(c, trajectory.Relativize(raw, "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Unevaluable == 0 {
		t.Error("a write outside the workspace was silently rewritten into something " +
			"evaluable; an escape must stay visible")
	}
}
