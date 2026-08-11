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

// evalCount is how many evals a drafted set asks for. It matches the full
// tier's budget, so a freshly generated set can be scored without an author
// having to add more.
const evalCount = 10

var suiteCmd = &cobra.Command{
	Use:   "suite",
	Short: "Work with a skill's eval set",
}

var suiteGenCmd = &cobra.Command{
	Use:   "gen <skill>",
	Short: "Draft the eval set for a skill and write it to evals/",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunSuiteGen(cmd.Context(), args[0])
	},
}

// RunSuiteGen drafts an eval set from a skill's approved spec and writes it
// into the skill's eval sidecar.
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

// generateSuite drafts and writes an eval set. Shared with `new`.
func generateSuite(ctx context.Context, st *store.Store, g llm.Gateway, sp *spec.SkillSpec) error {
	set, triggers, err := suite.Generate(ctx, g, sp, evalCount)
	if err != nil {
		return err
	}

	dir, err := st.SuiteDir(sp.Name)
	if err != nil {
		return err
	}
	if err := suite.WriteEvalSet(dir, set); err != nil {
		return err
	}
	if err := suite.WriteTriggerQueries(dir, triggers); err != nil {
		return err
	}

	// Record what was generated, so `suite push` can tell an untouched eval
	// set from one a person edited. A failure here must not fail generation:
	// the artifacts exist. The worst consequence is a push recorded as
	// authored, which is the status quo.
	if ref, rerr := suite.Ref(dir); rerr == nil {
		if err := st.RecordGeneratedRef(sp.Name, ref); err != nil {
			ui.Warn("could not record the generated eval set ref: %v", err)
		}
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{
			"skill":    sp.Name,
			"evals":    len(set.Evals),
			"triggers": len(triggers),
			"dir":      dir,
		})
	}
	ui.Success("wrote %d evals and %d trigger queries to %s", len(set.Evals), len(triggers), dir)
	return nil
}

func init() {
	suiteCmd.AddCommand(suiteGenCmd)
	rootCmd.AddCommand(suiteCmd)
}
