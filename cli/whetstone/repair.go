package whetstone

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/repair"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/ui"
)

// repairTier is the tier the repair loop evaluates against, for both dev
// iterations and the final holdout run. It must carry baseline runs — the
// audit that prunes non-discriminating and broken-in-both tasks needs both
// conditions measured — so TierSmoke (skill-only) will not do.
const repairTier = runner.TierFull

// bundleEvaluator implements repair.Evaluator over RunEvalWith, restricted
// to whatever task list it is asked to evaluate via EvalRequest.TaskFilter.
// It is the only place this package's whetstone eval and repair worlds
// meet: repair itself never imports runner, so this adapter is what keeps
// the loop testable with a scripted Evaluator instead.
type bundleEvaluator struct {
	deps  EvalDeps
	skill string
}

func (b *bundleEvaluator) EvaluateDev(ctx context.Context, tasks []suite.TaskPkg) (*repair.DevResult, error) {
	return b.evaluate(ctx, tasks)
}

func (b *bundleEvaluator) EvaluateHoldout(ctx context.Context, tasks []suite.TaskPkg) (*repair.DevResult, error) {
	return b.evaluate(ctx, tasks)
}

func (b *bundleEvaluator) evaluate(ctx context.Context, tasks []suite.TaskPkg) (*repair.DevResult, error) {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}

	rep, err := RunEvalWith(ctx, b.deps, EvalRequest{
		Skill: b.skill, Tier: repairTier, TaskFilter: ids, AllowVoid: true,
	})
	if err != nil {
		return nil, err
	}
	return devResultFromReport(rep), nil
}

// devResultFromReport turns a report.Report into repair.DevResult: the
// headline as Effectiveness, per-(task,model) pass/fail as ConditionResult
// (Audit's input), and contract violations plus failed skill runs as
// Failure (Cluster's input).
func devResultFromReport(rep *report.Report) *repair.DevResult {
	dr := &repair.DevResult{Effectiveness: rep.Headline}
	if rep.RobustnessGap != nil {
		dr.RobustnessGap = *rep.RobustnessGap
	}

	for _, t := range rep.Tasks {
		byModel := map[string]*repair.ConditionResult{}
		get := func(model string) *repair.ConditionResult {
			c, ok := byModel[model]
			if !ok {
				c = &repair.ConditionResult{TaskID: t.TaskID, Model: model}
				byModel[model] = c
			}
			return c
		}
		for _, c := range t.Conditions {
			passed := c.Runs > 0 && c.Passes > 0
			switch c.Condition {
			case runner.CondSkill:
				get(c.Model).SkillPassed = passed
				if !passed {
					dr.Failures = append(dr.Failures, repair.Failure{
						Kind: "verifier", ID: t.TaskID, TaskID: t.TaskID, Model: c.Model,
						Detail: fmt.Sprintf("%d/%d skill runs passed", c.Passes, c.Runs),
					})
				}
			case runner.CondBaseline:
				get(c.Model).BaselinePassed = passed
			}
		}
		for _, m := range byModel {
			dr.Conditions = append(dr.Conditions, *m)
		}

		for _, d := range t.Drift {
			for _, v := range d.Violations {
				detail := v.ID
				if len(v.Evidence) > 0 {
					detail = v.Evidence[0]
				}
				dr.Failures = append(dr.Failures, repair.Failure{
					Kind: "contract", ID: v.ID, TaskID: t.TaskID, Model: d.Model, Detail: detail,
				})
			}
		}
	}
	return dr
}

var (
	repairMaxIter int
	repairYes     bool
)

var repairCmd = &cobra.Command{
	Use:   "repair <skill>",
	Short: "Cluster failures, propose minimal edits, and re-evaluate until the dev split plateaus",
	Long: "Runs a repair loop against a skill's dev split: cluster observed failures, propose\n" +
		"minimal bundle edits, apply them, and re-run only the affected dev tasks. Stops at\n" +
		"a plateau or the iteration cap, then evaluates the holdout split exactly once — the\n" +
		"holdout score, not any dev-split score, is what gets reported.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunRepair(cmd.Context(), args[0], repairMaxIter, repairYes)
	},
}

// RunRepair resolves EvalDeps and a suite from the environment and runs the
// repair loop against skill.
func RunRepair(ctx context.Context, skill string, maxIter int, yes bool) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	gw, err := newGateway(st.Cache())
	if err != nil {
		return err
	}

	baseTag := os.Getenv("WHETSTONE_BASE_TAG")
	drv, err := docker.New(docker.Options{BaseTag: baseTag, Logger: ui.Info})
	if err != nil {
		return fmt.Errorf("whetstone repair: %w", err)
	}
	drv.Sweep(ctx)

	deps := EvalDeps{
		Store: st, Driver: drv, Gateway: gw, Adapters: agent.Get,
		Now: time.Now, Sleep: time.Sleep, EngineVersion: buildVersion,
	}

	sp, specVersion, err := st.LoadSpec(skill)
	if err != nil {
		return err
	}
	if !isApproved(st, skill, specVersion) {
		return fmt.Errorf("%s spec version %d is not approved; approve it with `whetstone spec approve %s`", skill, specVersion, skill)
	}

	suiteDir, err := st.SuiteDir(skill)
	if err != nil {
		return err
	}
	s, err := suite.Load(suiteDir)
	if err != nil {
		return fmt.Errorf("whetstone repair: loading suite for %s: %w (run `whetstone new %s` first)", skill, err, skill)
	}

	bundleDir, err := st.SkillDir(skill)
	if err != nil {
		return err
	}

	if !yes {
		approved, err := confirmRepair(skill, os.Stdin)
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("repair of %s was not approved", skill)
		}
	}

	loop, err := repair.NewLoop(repair.Options{Gateway: gw, MaxIter: maxIter, Logger: ui.Info})
	if err != nil {
		return err
	}

	res, err := loop.Run(ctx, &bundleEvaluator{deps: deps, skill: skill}, sp, bundleDir, s)
	if err != nil {
		return err
	}

	reportRepairResult(skill, res)
	return nil
}

// confirmRepair is the human gate: repair edits an authored bundle in
// place, so without --yes it asks before the first apply. A closed stdin is
// a decline, not an error — the same convention `whetstone new`'s
// confirmSpec uses, for the same reason: a non-interactive runner with no
// stdin must not be mistaken for one that broke.
func confirmRepair(skill string, in io.Reader) (bool, error) {
	fmt.Fprintf(os.Stderr, "  Repair will edit %s's bundle in place. Continue? [y/N] ", skill)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("whetstone repair: reading approval: %w", err)
	}
	answer := line
	for len(answer) > 0 && (answer[len(answer)-1] == '\n' || answer[len(answer)-1] == '\r') {
		answer = answer[:len(answer)-1]
	}
	switch answer {
	case "y", "Y", "yes", "Yes", "YES":
		return true, nil
	default:
		return false, nil
	}
}

func reportRepairResult(skill string, res *repair.LoopResult) {
	if ui.JSONMode {
		_ = ui.PrintJSON(map[string]any{
			"skill": skill, "iterations": res.Iterations, "holdout": res.Holdout,
			"stopped": res.Stopped, "pruned_tasks": res.PrunedTasks, "broken_tasks": res.BrokenTasks,
		})
		return
	}

	for _, it := range res.Iterations {
		ui.Info("iteration %d: dev effectiveness %.1f, %d proposal(s) applied", it.N, it.DevEffectiveness, it.Applied)
		for _, note := range it.Notes {
			ui.Info("  - %s", note)
		}
	}
	if len(res.PrunedTasks) > 0 {
		ui.Info("pruned non-discriminating tasks: %v", res.PrunedTasks)
	}
	if len(res.BrokenTasks) > 0 {
		ui.Info("routed broken tasks to the suite (not the skill): %v", res.BrokenTasks)
	}
	ui.Info("stopped: %s", res.Stopped)
	ui.Success("holdout effectiveness for %s: %.1f (this, not any dev-split score, is the reported number)", skill, res.Holdout)
}

func init() {
	repairCmd.Flags().IntVar(&repairMaxIter, "max-iter", repair.DefaultMaxIter, "Maximum repair iterations")
	repairCmd.Flags().BoolVar(&repairYes, "yes", false, "Skip the repair approval prompt")
	rootCmd.AddCommand(repairCmd)
}
