package whetstone

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/ui"
)

// splitSeed fixes the dev/holdout split. It is a constant rather than a flag
// because the holdout is what a reported score means: re-splitting with a
// different seed silently changes which tasks the repair loop was allowed to
// see, and makes two scores incomparable.
const splitSeed int64 = 1

var suiteCmd = &cobra.Command{
	Use:   "suite",
	Short: "Work with a skill's evaluation suite",
}

var suiteGenCmd = &cobra.Command{
	Use:   "gen <skill>",
	Short: "Generate and write the evaluation suite for a skill",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunSuiteGen(cmd.Context(), args[0])
	},
}

// RunSuiteGen drafts an evaluation suite from a skill's approved spec and
// writes it into the skill's eval sidecar.
func RunSuiteGen(ctx context.Context, skill string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	sp, err := loadApprovedSpec(st, skill)
	if err != nil {
		return err
	}

	g, err := newGateway(st.Cache())
	if err != nil {
		return err
	}
	return generateSuite(ctx, st, g, sp)
}

// generateSuite drafts, splits, and writes a suite. Shared with `new`.
func generateSuite(ctx context.Context, st *store.Store, g llm.Gateway, sp *spec.SkillSpec) error {
	s, dropped, err := suite.Generate(ctx, g, sp)
	if err != nil {
		return err
	}
	for _, d := range dropped {
		ui.Warn("whetstone suite gen: task %s could not be generated — %s", d.TaskID, d.Reason)
	}
	s.Split(splitSeed)

	dir, err := st.SuiteDir(sp.Name)
	if err != nil {
		return err
	}
	if err := s.Write(dir); err != nil {
		return err
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{
			"skill": sp.Name,
			"suite": dir,
			"tasks": len(s.Tasks),
		})
	}
	ui.Success("wrote %d tasks to %s", len(s.Tasks), dir)
	return nil
}

func init() {
	suiteCmd.AddCommand(suiteGenCmd)
	rootCmd.AddCommand(suiteCmd)
}
