package gate_test

import (
	"fmt"
	"testing"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findingCase is one shape of scan report: the class present at a blocking
// severity, if any.
type findingCase struct {
	name  string
	class string // "" means the report is clean
}

var findingCases = []findingCase{
	{"clean", ""},
	{"exfiltration", string(gate.ClassExfiltration)},
	{"secret", string(gate.ClassSecret)},
	{"execution", string(gate.ClassExecution)},
	{"injection", string(gate.ClassInjection)},
	{"heuristic", string(gate.ClassHeuristic)},
	{"unknown", "not-a-class"},
}

type qualityCase struct {
	name string
	q    *gate.QualityState
}

var qualityCases = []qualityCase{
	{"absent", nil},
	{"unverified", &gate.QualityState{Verified: false, PanelComplete: true, Headline: 90}},
	{"incomplete-panel", &gate.QualityState{Verified: true, PanelComplete: false, Headline: 90}},
	{"below-floor", &gate.QualityState{Verified: true, PanelComplete: true, Headline: 10}},
	{"at-floor", &gate.QualityState{Verified: true, PanelComplete: true, Headline: 50}},
	{"forbid-violations", &gate.QualityState{Verified: true, PanelComplete: true, Headline: 90, CriticalForbidViolations: 1}},
}

type policyCase struct {
	name string
	p    gate.Policy
}

var policyCases = []policyCase{
	{"floor0", gate.Policy{Floor: 0}},
	{"floor50", gate.Policy{Floor: 50}},
	{"floor0-override", gate.Policy{Floor: 0, AdminOverride: true}},
	{"floor50-override", gate.Policy{Floor: 50, AdminOverride: true}},
}

// clears reports whether q satisfies all four verification conditions.
func clears(q *gate.QualityState, floor float64) bool {
	return q != nil && q.Verified && q.PanelComplete &&
		q.Headline >= floor && q.CriticalForbidViolations == 0
}

// want is an independent restatement of the rules, written from the design
// document rather than from the implementation. If it were derived from the
// implementation it would agree with any bug.
func want(f findingCase, q *gate.QualityState, p gate.Policy) gate.Outcome {
	switch f.class {
	case "":
		return gate.Allow
	case string(gate.ClassExfiltration), string(gate.ClassSecret):
		return gate.Block
	case string(gate.ClassExecution), string(gate.ClassInjection), string(gate.ClassHeuristic):
		if clears(q, p.Floor) || p.AdminOverride {
			return gate.Allow
		}
		return gate.NeedsReview
	default:
		return gate.Block
	}
}

func report(class string) scan.Report {
	if class == "" {
		return scan.Report{Status: "clean"}
	}
	return scan.Report{
		Status: "critical",
		Findings: []scan.Finding{{
			Rule:     "test-rule",
			Severity: "critical",
			Class:    class,
			File:     "SKILL.md",
			Line:     3,
			Message:  "test finding",
		}},
		Summary: scan.Summary{Critical: 1},
	}
}

func TestDecideTable(t *testing.T) {
	const wantCases = 7 * 6 * 4
	seen := 0

	for _, f := range findingCases {
		for _, qc := range qualityCases {
			for _, pc := range policyCases {
				seen++
				name := fmt.Sprintf("%s/%s/%s", f.name, qc.name, pc.name)
				t.Run(name, func(t *testing.T) {
					got := gate.Decide(report(f.class), qc.q, pc.p)
					assert.Equal(t, want(f, qc.q, pc.p), got.Outcome)
				})
			}
		}
	}

	require.Equal(t, wantCases, seen,
		"the table must be the full product; adding a class or a quality state without extending it is the bug this assertion exists to catch")
}

// TestDecideOverrideNeverClearsUnappealable is stated separately because it
// is the single most important guarantee in the package and must not be
// readable as an accident of the table's shape.
func TestDecideOverrideNeverClearsUnappealable(t *testing.T) {
	perfect := &gate.QualityState{Verified: true, PanelComplete: true, Headline: 100}
	for _, class := range []gate.Class{gate.ClassExfiltration, gate.ClassSecret} {
		d := gate.Decide(report(string(class)), perfect, gate.Policy{Floor: 0, AdminOverride: true})
		assert.Equalf(t, gate.Block, d.Outcome,
			"%s must stay blocked with a perfect verified score AND an admin override", class)
	}
}

// TestDecideSeverityGates pins the choice that Phase 5 changes what happens
// to a block, not what counts as one. A medium-severity execution finding is
// what nearly every legitimate skill containing a shell command produces.
func TestDecideSeverityGates(t *testing.T) {
	rep := scan.Report{
		Status: "info",
		Findings: []scan.Finding{{
			Rule: "advisory", Severity: "medium", Class: string(gate.ClassExecution),
			File: "run.sh", Line: 1, Message: "advisory",
		}},
		Summary: scan.Summary{Medium: 1},
	}
	d := gate.Decide(rep, nil, gate.Policy{})
	assert.Equal(t, gate.AllowWithWarning, d.Outcome,
		"a non-blocking severity must stay advisory; routing it to review would block most real skills")
	assert.Len(t, d.Reasons, 1, "an advisory finding is still reported, just not blocking")
}

// TestDecideBlockWinsOverReview pins precedence when a report carries both.
func TestDecideBlockWinsOverReview(t *testing.T) {
	rep := scan.Report{
		Status: "critical",
		Findings: []scan.Finding{
			{Rule: "exec", Severity: "high", Class: string(gate.ClassExecution), File: "a", Line: 1},
			{Rule: "exfil", Severity: "critical", Class: string(gate.ClassExfiltration), File: "b", Line: 2},
		},
		Summary: scan.Summary{Critical: 1, High: 1},
	}
	perfect := &gate.QualityState{Verified: true, PanelComplete: true, Headline: 100}
	d := gate.Decide(rep, perfect, gate.Policy{AdminOverride: true})
	assert.Equal(t, gate.Block, d.Outcome)
}

// TestDecideReasonsExplainThemselves: Reasons is contract, not debug output —
// the CLI renders it and Phase 6's review screen consumes it.
func TestDecideReasonsExplainThemselves(t *testing.T) {
	d := gate.Decide(report(string(gate.ClassExecution)), nil, gate.Policy{Floor: 60})
	require.Len(t, d.Reasons, 1)
	r := d.Reasons[0]
	assert.Equal(t, "test-rule", r.Rule)
	assert.Equal(t, "SKILL.md", r.File)
	assert.Equal(t, 3, r.Line)
	assert.NotEmpty(t, r.Clears, "every reason must say what would clear it")
	assert.Contains(t, r.Clears, "60", "a floor-dependent reason must name the floor the caller is actually held to")
}

// TestDecideReasonsEmptyNotNil: Decision is marshalled into a JSONB column.
// json.Marshal on a nil slice emits null, which in a column holding an array
// makes "no reasons" indistinguishable from "never written".
func TestDecideReasonsEmptyNotNil(t *testing.T) {
	d := gate.Decide(scan.Report{Status: "clean"}, nil, gate.Policy{})
	assert.NotNil(t, d.Reasons, "Reasons must marshal as [] rather than null")
	assert.Empty(t, d.Reasons)
}
