package repair

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// DefaultMaxIter caps a repair loop at three passes over the dev split. Each
// pass is a full dev-split re-run; beyond three, the sessions spent chasing
// another point are not worth it.
const DefaultMaxIter = 3

// DefaultPlateauDelta is the minimum effectiveness gain, in points, that
// counts as progress. A gain smaller than this is noise, not a fix.
const DefaultPlateauDelta = 2.0

// DevResult is one evaluation's outcome against a task list: an
// effectiveness score, the failures that produced it (clustering input), the
// per-task/per-model pass/fail conditions (audit input), and the robustness
// gap (propose input).
type DevResult struct {
	Effectiveness float64
	Failures      []Failure
	Conditions    []ConditionResult
	RobustnessGap float64
}

// Evaluator runs an evaluation against a task list and reports its result.
// The loop drives evaluation entirely through this interface: the CLI
// implements it over the real eval path (internal/eval/runner), and tests
// implement it with scripted scores. No package imports both repair and
// runner.
type Evaluator interface {
	EvaluateDev(ctx context.Context, tasks []suite.TaskPkg) (*DevResult, error)
	EvaluateHoldout(ctx context.Context, tasks []suite.TaskPkg) (*DevResult, error)
}

// Options configures a repair loop.
type Options struct {
	Gateway llm.Gateway
	// MaxIter defaults to DefaultMaxIter when zero.
	MaxIter int
	// PlateauDelta defaults to DefaultPlateauDelta when zero.
	PlateauDelta float64
	// Logger defaults to a no-op.
	Logger func(string, ...any)
}

// Loop is a configured repair loop, ready to Run against a spec, a bundle,
// and a split suite.
type Loop struct {
	gateway      llm.Gateway
	maxIter      int
	plateauDelta float64
	logger       func(string, ...any)
}

// NewLoop validates o and returns a ready Loop.
func NewLoop(o Options) (*Loop, error) {
	if o.Gateway == nil {
		return nil, errors.New("repair: NewLoop needs a Gateway")
	}
	maxIter := o.MaxIter
	if maxIter == 0 {
		maxIter = DefaultMaxIter
	}
	plateauDelta := o.PlateauDelta
	if plateauDelta == 0 {
		plateauDelta = DefaultPlateauDelta
	}
	logger := o.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &Loop{gateway: o.Gateway, maxIter: maxIter, plateauDelta: plateauDelta, logger: logger}, nil
}

// LoopResult is a repair loop's outcome: every iteration's dev-split result,
// the holdout score measured once at the end, why the loop stopped, and
// which tasks were pruned or found broken along the way.
type LoopResult struct {
	Iterations []report.Iteration
	// Holdout is the reported score. It is measured exactly once, against
	// the split the loop never saw, after the loop has stopped.
	Holdout float64
	// Stopped is "plateau" or "maxiter".
	Stopped     string
	PrunedTasks []string
	BrokenTasks []string
}

// Run drives the repair loop: cluster failures, propose minimal edits,
// apply them, re-run only the affected dev tasks, and repeat until the gain
// plateaus or the iteration cap is reached. The holdout split never enters
// this loop — it is neither evaluated nor put in front of the model — and is
// scored exactly once at the end, because a suite the loop can see is a
// suite the loop will fit.
func (l *Loop) Run(ctx context.Context, e Evaluator, sp *spec.SkillSpec, bundleDir string, s *suite.Suite) (*LoopResult, error) {
	var devTasks, holdoutTasks []suite.TaskPkg
	for _, t := range s.Tasks {
		switch t.Split {
		case "holdout":
			holdoutTasks = append(holdoutTasks, t)
		default:
			// Anything not explicitly "holdout" (including an unsplit "")
			// is treated as dev — the loop must never silently drop a task
			// that was never split.
			devTasks = append(devTasks, t)
		}
	}

	res := &LoopResult{}
	prunedSet := map[string]bool{}
	brokenSet := map[string]bool{}

	active := devTasks
	var prevScore *float64
	var triedDeletion bool

	for n := 1; n <= l.maxIter; n++ {
		devRes, err := e.EvaluateDev(ctx, active)
		if err != nil {
			return nil, fmt.Errorf("repair: iteration %d: evaluating dev split: %w", n, err)
		}

		audit := AuditAssertions(devRes.Conditions)
		for _, id := range audit.NonDiscriminating {
			prunedSet[id] = true
		}
		for _, id := range audit.BrokenInBoth {
			brokenSet[id] = true
		}
		nextActive := PruneTasks(audit, active)

		var gain float64
		plateaued := false
		if prevScore != nil {
			gain = devRes.Effectiveness - *prevScore
			plateaued = gain < l.plateauDelta
		}

		// The over-constraint probe fires before the loop gives up: the
		// first time a plateau is detected, this iteration gets one
		// deletion attempt rather than an immediate stop. Only a second
		// consecutive plateau — after that attempt has already had its
		// chance to move the score — ends the loop.
		if plateaued && triedDeletion {
			res.Stopped = "plateau"
			break
		}
		allowDeletion := plateaued
		if plateaued {
			triedDeletion = true
		}

		failures := make([]Failure, 0, len(devRes.Failures))
		for _, f := range devRes.Failures {
			if brokenSet[f.TaskID] {
				continue
			}
			failures = append(failures, f)
		}
		clusters := Cluster(failures)

		var applied []Proposal
		var notes []string
		if len(clusters) > 0 {
			proposals, perr := Propose(ctx, l.gateway, ProposeInput{
				Spec: sp, BundleDir: bundleDir, Clusters: clusters,
				RobustnessGap: devRes.RobustnessGap, AllowDeletion: allowDeletion,
			})
			if perr != nil {
				return nil, fmt.Errorf("repair: iteration %d: proposing: %w", n, perr)
			}
			if len(proposals) > 0 {
				if err := Apply(bundleDir, proposals); err != nil {
					return nil, fmt.Errorf("repair: iteration %d: applying: %w", n, err)
				}
			}
			applied = proposals
			for _, p := range applied {
				notes = append(notes, fmt.Sprintf("%s: %s", p.File, p.Rationale))
			}
		}

		iterPruned := append(append([]string{}, audit.NonDiscriminating...), audit.BrokenInBoth...)
		sort.Strings(iterPruned)
		res.Iterations = append(res.Iterations, report.Iteration{
			N: n, DevEffectiveness: devRes.Effectiveness, Applied: len(applied), Pruned: iterPruned, Notes: notes,
		})
		l.logger("repair: iteration %d: dev effectiveness %.1f, %d proposal(s) applied", n, devRes.Effectiveness, len(applied))

		score := devRes.Effectiveness
		prevScore = &score
		active = nextActive

		if n == l.maxIter {
			res.Stopped = "maxiter"
		}
	}
	if res.Stopped == "" {
		res.Stopped = "maxiter"
	}

	res.PrunedTasks = sortedKeys(prunedSet)
	res.BrokenTasks = sortedKeys(brokenSet)

	holdoutRes, err := e.EvaluateHoldout(ctx, holdoutTasks)
	if err != nil {
		return nil, fmt.Errorf("repair: evaluating holdout: %w", err)
	}
	res.Holdout = holdoutRes.Effectiveness

	return res, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
