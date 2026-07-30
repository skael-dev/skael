// Package runner plans and executes an evaluation: which sessions to run, in
// what environment, with what resumed from a previous attempt.
//
// Planning is separated from execution and has no I/O, so the session budgets
// are arithmetic a test can check rather than a cost discovered afterwards.
package runner

import (
	"fmt"
	"sort"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// Tier names an evaluation's depth.
type Tier string

const (
	// TierSmoke is immediate feedback: five development tasks on the primary
	// member, no baselines, no probes.
	TierSmoke Tier = "smoke"
	// TierFull is the reportable tier: 60 core sessions plus 16 trigger probes.
	TierFull Tier = "full"
	// TierDeep is opt-in and takes hours.
	TierDeep Tier = "deep"
)

// Conditions a session runs under. The skill/baseline pair is what Uplift
// compares; a trigger probe is a short session measuring only whether the skill
// fired.
const (
	CondSkill    = "skill"
	CondBaseline = "baseline"
	CondTrigger  = "trigger"
)

// Member is one entry in the model panel.
type Member struct {
	Agent string
	Model string
	// Class is the capability tier this member stands for. RobustnessGap is the
	// difference between the strong member and the floor member, so the class
	// is part of the panel's identity rather than a label.
	Class spec.ModelTier
}

// Panel is the set of members every task is run against.
type Panel []Member

// tierBudget is §9's table as data. Each field is a hard cap: a tier that
// cannot be filled is an error, because a smaller run reported under a tier's
// name is a claim nothing downstream can verify.
type tierBudget struct {
	Tasks        int
	Runs         int // attempts per task per member
	BaselineRuns int
	Probes       int // positive + negative, on the primary member
	N, K         int
	DevOnly      bool
	PrimaryOnly  bool
}

var budgets = map[Tier]tierBudget{
	TierSmoke: {Tasks: 5, Runs: 1, BaselineRuns: 0, Probes: 0, N: 1, K: 1, DevOnly: true, PrimaryOnly: true},
	TierFull:  {Tasks: 10, Runs: 2, BaselineRuns: 1, Probes: 16, N: 2, K: 2},
	TierDeep:  {Tasks: 16, Runs: 4, BaselineRuns: 2, Probes: 24, N: 4, K: 3},
}

// DefaultPanel is the shipped panel: one strong member and one floor member of
// the same vendor.
//
// Two capability tiers rather than two vendors, for a recorded reason: the
// second vendor's CLI is unauthenticated on the machine this was built on, and
// a parser written from documentation passes its own tests and fails on real
// output. min-across-panel and RobustnessGap need a strong/floor pair, which
// this provides; the cross-vendor claim stays unmade until a real stream can be
// recorded.
func DefaultPanel() Panel {
	return Panel{
		{Agent: "claude-code", Model: "opus", Class: spec.TierStrong},
		{Agent: "claude-code", Model: "haiku", Class: spec.TierFloor},
	}
}

// ParsePanel builds a panel from CLI arguments, cross-checking each agent
// against the adapter registry so a typo is a refusal rather than a panel
// member that silently never runs.
func ParsePanel(agents, models []string) (Panel, error) {
	if len(agents) == 0 || len(models) == 0 {
		return DefaultPanel(), nil
	}
	var p Panel
	for _, a := range agents {
		if _, ok := agent.Get(a); !ok {
			return nil, fmt.Errorf("runner: no adapter named %q (adapters register from a blank import; run `whetstone doctor` to list them)", a)
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
	// N is runs per task per member; K is the passes required by the
	// Reliability estimator.
	N, K  int
	Tasks []suite.TaskPkg
}

// BuildPlan enumerates the sessions for a tier.
func BuildPlan(t Tier, p Panel, s *suite.Suite, void map[string]bool) (*Plan, error) {
	b, ok := budgets[t]
	if !ok {
		return nil, fmt.Errorf("runner: unknown tier %q", t)
	}
	if len(p) == 0 {
		return nil, fmt.Errorf("runner: tier %s needs at least one panel member", t)
	}

	eligible := make([]suite.TaskPkg, 0, len(s.Tasks))
	for _, task := range s.Tasks {
		if void[task.ID] {
			continue
		}
		if b.DevOnly && task.Split == "holdout" {
			continue
		}
		eligible = append(eligible, task)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })

	if len(eligible) < b.Tasks {
		return nil, fmt.Errorf("runner: tier %s needs %d eligible tasks, the suite has %d (void or holdout tasks are excluded); regenerate the suite or run a smaller tier",
			t, b.Tasks, len(eligible))
	}
	eligible = eligible[:b.Tasks]

	members := p
	if b.PrimaryOnly {
		members = p[:1]
	}

	plan := &Plan{Tier: t, Panel: p, N: b.N, K: b.K, Tasks: eligible}
	for _, task := range eligible {
		for _, m := range members {
			for attempt := 1; attempt <= b.Runs; attempt++ {
				plan.Runs = append(plan.Runs, store.RunKey{
					TaskID: task.ID, Agent: m.Agent, Model: m.Model, Condition: CondSkill, Attempt: attempt,
				})
			}
			for attempt := 1; attempt <= b.BaselineRuns; attempt++ {
				plan.Runs = append(plan.Runs, store.RunKey{
					TaskID: task.ID, Agent: m.Agent, Model: m.Model, Condition: CondBaseline, Attempt: attempt,
				})
			}
		}
	}

	if b.Probes > 0 {
		primary := p[0]
		half := b.Probes / 2
		for i := 0; i < half && i < len(s.Triggers.Positive); i++ {
			plan.Probes = append(plan.Probes, Probe{Index: i, Prompt: s.Triggers.Positive[i], Positive: true, Member: primary})
		}
		for i := 0; i < half && i < len(s.Triggers.Negative); i++ {
			plan.Probes = append(plan.Probes, Probe{Index: half + i, Prompt: s.Triggers.Negative[i], Positive: false, Member: primary})
		}
		if len(plan.Probes) < b.Probes {
			return nil, fmt.Errorf("runner: tier %s needs %d trigger prompts, the suite has %d positive and %d negative",
				t, b.Probes, len(s.Triggers.Positive), len(s.Triggers.Negative))
		}
	}
	return plan, nil
}

// SessionCount is the total number of agent sessions the plan will spend.
func (p *Plan) SessionCount() int { return len(p.Runs) + len(p.Probes) }

// DevRuns and HoldoutRuns partition the plan by split. The repair loop iterates
// on DevRuns and reports HoldoutRuns, and it must never see the latter.
func (p *Plan) DevRuns() []store.RunKey     { return p.runsInSplit("dev") }
func (p *Plan) HoldoutRuns() []store.RunKey { return p.runsInSplit("holdout") }

func (p *Plan) runsInSplit(split string) []store.RunKey {
	in := map[string]bool{}
	for _, t := range p.Tasks {
		if t.Split == split {
			in[t.ID] = true
		}
	}
	var out []store.RunKey
	for _, k := range p.Runs {
		if in[k.TaskID] {
			out = append(out, k)
		}
	}
	return out
}
