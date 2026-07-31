package drift_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

func ev(seq int, typ trajectory.EventType, name string, paths ...string) trajectory.Event {
	return trajectory.Event{Seq: seq, Type: typ, Name: name, Paths: paths}
}

func TestObserve_MatchesAStepByShellPattern(t *testing.T) {
	c := &contract.Contract{Version: 1, Steps: []contract.StepMatch{{
		ID: "s1", Match: contract.Matcher{Type: trajectory.TypeShell, Pattern: `scripts/parse\.py`}, Order: contract.Order{Mode: "any"}, Required: true,
	}}}
	o, err := drift.Observe(c, []trajectory.Event{ev(1, trajectory.TypeShell, "python3 scripts/parse.py data.csv")})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Steps) != 1 || !o.Steps[0].Matched {
		t.Errorf("steps = %+v, want s1 matched", o.Steps)
	}
}

func TestObserve_APathGlobIsRecursiveUnderTheContractDialect(t *testing.T) {
	c := &contract.Contract{Version: 1, Steps: []contract.StepMatch{{
		ID: "s1", Match: contract.Matcher{Type: trajectory.TypeFileWrite, PathGlob: "out/**"}, Order: contract.Order{Mode: "any"}, Required: true,
	}}}
	// filepath.Match("out/**", "out/tables/q1.csv") is false: its "**" is two
	// ordinary stars, neither of which crosses a "/". Using it here scores a
	// skill that wrote exactly where it was told as having skipped the step.
	o, err := drift.Observe(c, []trajectory.Event{ev(1, trajectory.TypeFileWrite, "Write", "out/tables/q1.csv")})
	if err != nil {
		t.Fatal(err)
	}
	if !o.Steps[0].Matched {
		t.Error("a nested write under out/** did not match; MatchPath is not being used")
	}
}

func TestObserve_CountsAForbidHitAndQuotesIt(t *testing.T) {
	c := &contract.Contract{Version: 1, Forbid: []contract.ForbidMatch{{
		ID: "f1", Match: contract.Matcher{Type: trajectory.TypeFileWrite, PathNotGlob: "out/**"}, Severity: spec.SeverityCritical,
	}}}
	o, err := drift.Observe(c, []trajectory.Event{
		ev(1, trajectory.TypeFileWrite, "Write", "out/ok.csv"),
		ev(2, trajectory.TypeFileWrite, "Write", "/tmp/../etc/sneaky"),
		ev(3, trajectory.TypeFileWrite, "Write", "elsewhere/bad.csv"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Violations) != 1 {
		t.Fatalf("violations = %+v", o.Violations)
	}
	// Two of the three writes are outside out/. The absolute one is a reported
	// error from MatchPath rather than a quiet non-match, so it must not vanish.
	if o.Violations[0].Hits < 1 {
		t.Errorf("hits = %d", o.Violations[0].Hits)
	}
	if len(o.Violations[0].Evidence) == 0 {
		t.Error("a violation with no evidence cannot be checked by whoever reads the report")
	}
}

func TestObserve_AnAbsolutePathIsUnevaluableAndVisible(t *testing.T) {
	c := &contract.Contract{Version: 1, Forbid: []contract.ForbidMatch{{
		ID: "f1", Match: contract.Matcher{Type: trajectory.TypeFileWrite, PathNotGlob: "out/**"}, Severity: spec.SeverityMajor,
	}}}
	o, err := drift.Observe(c, []trajectory.Event{ev(1, trajectory.TypeFileWrite, "Write", "/etc/passwd")})
	if err != nil {
		t.Fatalf("Observe returned an error for an absolute candidate: %v", err)
	}
	// MatchPath reports an absolute candidate loudly because something upstream
	// lost the workspace root. Swallowing it as "not a violation" is a missed
	// violation, and a missed violation looks like a clean run.
	if o.Unevaluable == 0 {
		t.Error("an unevaluable path was silently dropped")
	}
	if len(o.UnevaluableDetail) == 0 || !strings.Contains(strings.Join(o.UnevaluableDetail, " "), "/etc/passwd") {
		t.Errorf("detail = %v, want the offending path named", o.UnevaluableDetail)
	}
}

func TestObserve_AMalformedPatternIsAnError(t *testing.T) {
	c := &contract.Contract{Version: 1, Forbid: []contract.ForbidMatch{{
		ID: "f1", Match: contract.Matcher{Type: trajectory.TypeFileWrite, PathNotGlob: "a/**/b"}, Severity: spec.SeverityMajor,
	}}}
	// A pattern the dialect rejects is a compiler defect. Scoring around it
	// produces a number, which is worse than producing nothing.
	if _, err := drift.Observe(c, []trajectory.Event{ev(1, trajectory.TypeFileWrite, "Write", "x")}); err == nil {
		t.Error("Observe accepted a malformed path pattern")
	}
}

func TestObserve_OrderIsRecordedAgainstTheAfterConstraint(t *testing.T) {
	c := &contract.Contract{Version: 1, Steps: []contract.StepMatch{
		{ID: "s1", Match: contract.Matcher{Type: trajectory.TypeFileRead, PathGlob: "data.csv"}, Order: contract.Order{Mode: "any"}, Required: true},
		{ID: "s2", Match: contract.Matcher{Type: trajectory.TypeFileWrite, PathGlob: "out/**"}, Order: contract.Order{Mode: "after", After: []string{"s1"}}, Required: true},
	}}
	out, err := drift.Observe(c, []trajectory.Event{
		ev(1, trajectory.TypeFileWrite, "Write", "out/x.csv"),
		ev(2, trajectory.TypeFileRead, "Read", "data.csv"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var s2 drift.StepObs
	for _, s := range out.Steps {
		if s.ID == "s2" {
			s2 = s
		}
	}
	if !s2.Matched {
		t.Fatal("s2 not matched")
	}
	// Writing the output before reading the input is the step happening, out of
	// order. Both facts are needed: coverage counts it, OrderScore penalises it.
	if s2.OrderOK {
		t.Error("OrderOK = true for a step that ran before its predecessor")
	}
}

func TestObserve_OpaqueEventsAreExcludedFromEveryDenominator(t *testing.T) {
	c := &contract.Contract{Version: 1, Steps: []contract.StepMatch{{
		ID: "s1", Match: contract.Matcher{Type: trajectory.TypeShell, Pattern: `parse\.py`}, Order: contract.Order{Mode: "any"}, Required: true,
	}}}
	o, err := drift.Observe(c, []trajectory.Event{
		ev(1, trajectory.TypeShell, "python3 parse.py"),
		ev(2, trajectory.TypeOpaque, "SomeNewTool"),
		ev(3, trajectory.TypeOpaque, "AnotherNewTool"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// An unmapped event is a parser gap, not misbehaviour. Counting it as
	// off-contract makes FocusScore fall whenever the CLI ships a new tool.
	if o.OffContract != 0 {
		t.Errorf("OffContract = %d, want 0: opaque events are recorded, never scored", o.OffContract)
	}
	if o.Total != 1 {
		t.Errorf("Total = %d, want 1 contractable event", o.Total)
	}
}

func TestObserve_CheckpointsRecordWhetherTheirStepRan(t *testing.T) {
	c := &contract.Contract{
		Version: 1,
		Steps: []contract.StepMatch{
			{ID: "s1", Match: contract.Matcher{Type: trajectory.TypeShell, Pattern: `validate\.py`}, Order: contract.Order{Mode: "any"}, Required: true},
			{ID: "s2", Match: contract.Matcher{Type: trajectory.TypeShell, Pattern: `report\.py`}, Order: contract.Order{Mode: "any"}, Required: true},
		},
		Checkpoints: []string{"s1", "s2"},
	}
	o, err := drift.Observe(c, []trajectory.Event{ev(1, trajectory.TypeShell, "python3 validate.py")})
	if err != nil {
		t.Fatal(err)
	}
	if !o.Checkpoints["s1"] || o.Checkpoints["s2"] {
		t.Errorf("checkpoints = %v, want s1 run and s2 not", o.Checkpoints)
	}
}
