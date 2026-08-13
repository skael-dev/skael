package whetstone

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/tune"
	"github.com/skael-dev/skael/internal/ui"
)

// The shipped defaults. Anthropic's run_loop.py defaults to 20 queries, 3 runs
// and 5 iterations, which is 300 model calls to tune one sentence. Sixteen
// queries are also what the deep tier needs. Two runs are the floor that
// still separates a flaky decision from a real failure. At one run the loop
// rewrites a description to fix a coin flip.
const (
	defaultTuneQueries    = 16
	defaultTuneRuns       = 2
	defaultTuneIterations = 3
	defaultTuneHoldout    = 0.4
	defaultTuneThreshold  = 0.5
)

// TuneRequest is one `whetstone tune` invocation.
type TuneRequest struct {
	Skill       string
	Queries     int
	Runs        int
	Iterations  int
	Concurrency int
	Threshold   float64
	Holdout     float64
	// Apply writes the winner to the spec and to SKILL.md. A false value
	// still measures the description. It still reports what it found. It
	// changes nothing.
	Apply bool
}

// RunTuneWith tunes a skill's description against its trigger set.
func RunTuneWith(ctx context.Context, st *store.Store, g llm.Gateway, req TuneRequest) (*tune.Result, error) {
	if g == nil {
		return nil, fmt.Errorf("whetstone tune: needs an LLM gateway; run `whetstone doctor` to check your setup")
	}

	sp, _, err := st.LoadSpec(req.Skill)
	if err != nil {
		return nil, err
	}
	skillDir, err := st.SkillDir(req.Skill)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("whetstone tune: reading SKILL.md for %s: %w (run `whetstone gen %s` first)", req.Skill, err, req.Skill)
	}

	suiteDir, err := st.SuiteDir(req.Skill)
	if err != nil {
		return nil, err
	}
	queries, err := suite.LoadTriggerQueries(suiteDir)
	if err != nil {
		return nil, err
	}

	grown, err := tune.TopUp(ctx, g, sp.Name, sp.Description, string(body), queries, req.Queries)
	if err != nil {
		return nil, err
	}
	if len(grown) != len(queries) {
		if req.Apply {
			// The eval tiers read the same file, so a set grown only in
			// memory helps nothing but this one run.
			if err := suite.WriteTriggerQueries(suiteDir, grown); err != nil {
				return nil, err
			}
			// The write lands inside the directory suite.Ref hashes, so the
			// recorded ref is now stale. Leave it stale and the next push
			// declares nothing, which records this eval set as authored.
			recordGeneratedRef(st, req.Skill, suiteDir)
			ui.Info("grew the trigger set from %d to %d queries", len(queries), len(grown))
		} else {
			ui.Info("would grow the trigger set from %d to %d queries; rerun with --apply to write it", len(queries), len(grown))
		}
	}

	distractors, err := suite.Distractors()
	if err != nil {
		return nil, err
	}

	res, err := tune.Run(ctx, g, tune.Options{
		SkillName: sp.Name, SkillBody: string(body), Description: sp.Description,
		Queries: grown, Distractors: distractors,
		Runs: req.Runs, Iterations: req.Iterations, Concurrency: req.Concurrency,
		Threshold: req.Threshold, Holdout: req.Holdout, Seed: 42,
		Log: func(format string, args ...any) { ui.Info(format, args...) },
	})
	if err != nil {
		return nil, err
	}

	if req.Apply && res.Best != sp.Description {
		// The spec first, then the bundle. The generator writes the
		// frontmatter from the spec, so the next `whetstone gen` overwrites a
		// bundle patched on its own.
		sp.Description = res.Best
		version, serr := st.SaveSpec(sp)
		if serr != nil {
			return nil, serr
		}
		if serr := st.ApproveSpec(sp.Name, version); serr != nil {
			return nil, serr
		}
		if serr := gen.RewriteDescription(skillDir, res.Best); serr != nil {
			return nil, serr
		}
		ui.Success("stored and approved %s spec version %d with the tuned description", sp.Name, version)
	}

	if ui.JSONMode {
		if err := ui.PrintJSON(res); err != nil {
			return nil, err
		}
		return &res, nil
	}
	fmt.Fprintf(os.Stderr, "\n  before: %s\n  after:  %s\n  train %s, held out %s, %d iterations (%s)\n\n",
		res.Original, res.Best, res.BestTrain, res.BestTest, res.Iterations, res.ExitReason)
	ui.Info("confirm the change against real agent sessions with %s", ui.Code("whetstone eval "+req.Skill))
	return &res, nil
}

// RunTune resolves the store and the gateway from the environment. It calls
// RunTuneWith with them.
func RunTune(ctx context.Context, req TuneRequest) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	g, err := newGateway(st.Cache())
	if err != nil {
		return err
	}
	_, err = RunTuneWith(ctx, st, g, req)
	return err
}

var tuneReq = TuneRequest{Apply: true}

var tuneCmd = &cobra.Command{
	Use:   "tune <skill>",
	Short: "Tune a skill's description for triggering accuracy",
	Long: "Measure how often the description makes a model consult this skill, propose\n" +
		"a better one from what failed, and keep the version that scores best on the\n" +
		"queries it was never tuned against.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tuneReq.Skill = args[0]
		return RunTune(cmd.Context(), tuneReq)
	},
}

func init() {
	tuneCmd.Flags().IntVar(&tuneReq.Queries, "queries", defaultTuneQueries, "Trigger queries to tune against; a short set is topped up and written back")
	tuneCmd.Flags().IntVar(&tuneReq.Runs, "runs", defaultTuneRuns, "Runs per query")
	tuneCmd.Flags().IntVar(&tuneReq.Iterations, "iterations", defaultTuneIterations, "Maximum improvement iterations")
	tuneCmd.Flags().IntVar(&tuneReq.Concurrency, "concurrency", 0, "Maximum concurrent model calls (0 uses the default)")
	tuneCmd.Flags().Float64Var(&tuneReq.Threshold, "threshold", defaultTuneThreshold, "Trigger rate at which a query counts as fired")
	tuneCmd.Flags().Float64Var(&tuneReq.Holdout, "holdout", defaultTuneHoldout, "Fraction of the set held out for selection; 0 disables it")
	tuneCmd.Flags().BoolVar(&tuneReq.Apply, "apply", true, "Write the winner to the spec and to SKILL.md")
	rootCmd.AddCommand(tuneCmd)
}
