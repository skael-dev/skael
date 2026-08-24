// Package runner plans and executes an evaluation: which sessions to run, in
// what environment, with what resumed from a previous attempt.
//
// Planning is separated from execution and has no I/O, so the session budgets
// are arithmetic a test can check rather than a cost discovered afterwards.
package runner

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// Tier names an evaluation's depth.
type Tier string

const (
	// TierSmoke is immediate feedback: five evals on one member, no baseline
	// and no probes.
	TierSmoke Tier = "smoke"
	// TierFull is the reportable tier: 30 task sessions on one member plus a
	// six-query trigger smoke check.
	TierFull Tier = "full"
	// TierDeep adds the floor member and a third attempt per eval.
	TierDeep Tier = "deep"
)

// Conditions a session runs under. The skill/baseline pair is what the
// published delta compares; a trigger probe is a short session measuring only
// whether the skill fired.
const (
	CondSkill    store.Condition = "skill"
	CondBaseline store.Condition = "baseline"
	CondTrigger  store.Condition = "trigger"
)

// Member is one entry in the model panel.
type Member struct {
	Agent string
	Model string
	Class spec.ModelTier
}

// Panel is the set of members every eval is run against.
type Panel []Member

// tierBudget is each tier's session count, as data. Every field is a hard cap:
// a tier that cannot be filled is an error, because a smaller run reported
// under a tier's name is a claim nothing downstream can verify.
type tierBudget struct {
	Evals int
	Runs  int // attempts per eval per member, with the skill
	// BaselineRuns is attempts per eval with no skill installed, on the primary
	// member only. The delta is one published number answering one question, so
	// a per-member baseline buys nothing and costs a session per eval per
	// member.
	BaselineRuns int
	// Probes is the trigger smoke check: how many queries, split evenly
	// between should-trigger and should-not. The full trigger measurement is a
	// separate command; this only has to answer "does it fire at all", which is
	// what the release precondition needs.
	Probes      int
	PrimaryOnly bool
}

var budgets = map[Tier]tierBudget{
	TierSmoke: {Evals: 5, Runs: 1, BaselineRuns: 0, Probes: 0, PrimaryOnly: true},
	TierFull:  {Evals: 10, Runs: 2, BaselineRuns: 1, Probes: 6, PrimaryOnly: true},
	TierDeep:  {Evals: 16, Runs: 3, BaselineRuns: 1, Probes: 16, PrimaryOnly: false},
}

// DefaultPanel is the shipped panel: one member.
//
// Sonnet rather than Opus, because it is what most people running claude-code
// actually run — so it is a truer measurement, not merely a cheaper one. The
// floor member joins only at TierDeep: "works on the weakest model" is worth
// measuring, and it is not worth doubling every score's cost to measure.
func DefaultPanel() Panel {
	return Panel{{Agent: "claude-code", Model: "sonnet", Class: spec.TierStrong}}
}

// DeepPanel adds the floor member.
func DeepPanel() Panel {
	return Panel{
		{Agent: "claude-code", Model: "sonnet", Class: spec.TierStrong},
		{Agent: "claude-code", Model: "haiku", Class: spec.TierFloor},
	}
}

// PanelFor returns the shipped panel for a tier.
func PanelFor(t Tier) Panel {
	if t == TierDeep {
		return DeepPanel()
	}
	return DefaultPanel()
}

// ParsePanel builds a panel from caller arguments, cross-checking each agent
// against the adapter lookup so a typo is a refusal rather than a panel member
// that silently never runs. The first model is the strong member and any
// others are floor members.
//
// Models alone are enough. The UI and the re-run endpoint offer a model
// without an agent — there is one adapter to choose from — and requiring both
// meant a chosen model fell back to the shipped panel with no error anywhere,
// producing a score against a model nobody asked for.
func ParsePanel(agents, models []string) (Panel, error) {
	if len(models) == 0 {
		return DefaultPanel(), nil
	}
	if len(agents) == 0 {
		agents = []string{DefaultPanel()[0].Agent}
	}
	var p Panel
	for _, a := range agents {
		if _, ok := agent.Get(a); !ok {
			return nil, fmt.Errorf("runner: no adapter named %q (run `whetstone doctor` to list them)", a)
		}
		for i, m := range models {
			class := spec.TierStrong
			if i > 0 {
				class = spec.TierFloor
			}
			p = append(p, Member{Agent: a, Model: m, Class: class})
		}
	}
	return p, nil
}

// Probe is one trigger measurement: a short session that answers only whether
// the skill fired.
type Probe struct {
	Index    int
	Prompt   string
	Positive bool
	Member   Member
}

// Plan is the complete list of work an evaluation performs.
type Plan struct {
	Tier   Tier
	Panel  Panel
	Runs   []store.RunKey
	Probes []Probe
	Evals  []suite.Eval
}

// EvalID renders an eval's id as the run key's task id. Run keys are stored as
// text and predate evals.json, so the conversion happens in one place.
func EvalID(id int) string { return strconv.Itoa(id) }

// BuildPlan enumerates the sessions for a tier. void lists evals excluded by
// suite.Validate, which are planned by nobody and scored by nobody.
func BuildPlan(t Tier, p Panel, set *suite.EvalSet, void map[int]bool, triggers []suite.TriggerQuery) (*Plan, error) {
	b, ok := budgets[t]
	if !ok {
		return nil, fmt.Errorf("runner: unknown tier %q", t)
	}
	if len(p) == 0 {
		return nil, fmt.Errorf("runner: tier %s needs at least one panel member", t)
	}
	if set == nil {
		return nil, fmt.Errorf("runner: tier %s needs an eval set", t)
	}

	eligible := make([]suite.Eval, 0, len(set.Evals))
	for _, e := range set.Evals {
		if !void[e.ID] {
			eligible = append(eligible, e)
		}
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })

	if len(eligible) < b.Evals {
		return nil, fmt.Errorf("runner: tier %s needs %d evals, the set has %d that can be scored (void evals are excluded); add evals or run a smaller tier",
			t, b.Evals, len(eligible))
	}
	selected := eligible[:b.Evals]

	members := p
	if b.PrimaryOnly {
		members = p[:1]
	}
	primary := p[0]

	plan := &Plan{Tier: t, Panel: p, Evals: selected}
	for _, e := range selected {
		id := EvalID(e.ID)
		for _, m := range members {
			for attempt := 1; attempt <= b.Runs; attempt++ {
				plan.Runs = append(plan.Runs, store.RunKey{
					TaskID: id, Agent: m.Agent, Model: m.Model, Condition: CondSkill, Attempt: attempt,
				})
			}
		}
		for attempt := 1; attempt <= b.BaselineRuns; attempt++ {
			plan.Runs = append(plan.Runs, store.RunKey{
				TaskID: id, Agent: primary.Agent, Model: primary.Model, Condition: CondBaseline, Attempt: attempt,
			})
		}
	}

	if b.Probes > 0 {
		half := b.Probes / 2
		var positive, negative []suite.TriggerQuery
		for _, q := range triggers {
			if q.ShouldTrigger {
				positive = append(positive, q)
			} else {
				negative = append(negative, q)
			}
		}
		if len(positive) < half || len(negative) < half {
			return nil, fmt.Errorf("runner: tier %s needs %d should-trigger and %d should-not-trigger queries, evals/triggers.json has %d and %d",
				t, half, half, len(positive), len(negative))
		}
		for i := 0; i < half; i++ {
			plan.Probes = append(plan.Probes,
				Probe{Index: i, Prompt: positive[i].Query, Positive: true, Member: primary})
		}
		for i := 0; i < half; i++ {
			plan.Probes = append(plan.Probes,
				Probe{Index: half + i, Prompt: negative[i].Query, Positive: false, Member: primary})
		}
	}
	return plan, nil
}

// EvalByID returns the planned eval with the given run-key task id.
func (p *Plan) EvalByID(taskID string) (suite.Eval, bool) {
	for _, e := range p.Evals {
		if EvalID(e.ID) == taskID {
			return e, true
		}
	}
	return suite.Eval{}, false
}

// BaselinePlanned reports whether the plan runs any baseline session, which is
// what makes the without-skill delta measurable.
func BaselinePlanned(p Plan) bool {
	for _, k := range p.Runs {
		if k.Condition == CondBaseline {
			return true
		}
	}
	return false
}
