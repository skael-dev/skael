package drift_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// The regression test for the bug the corpus could not see: its fixtures are
// hand-authored with relative paths ("out/report.md"), while a real agent
// stream reports absolute container paths, which MatchPath rejects. Every
// path-bearing rule was unevaluable in production while the tests stayed
// green.
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
