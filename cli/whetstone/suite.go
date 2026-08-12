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

	recordGeneratedRef(st, sp.Name, dir)

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

// recordGeneratedRef records the content ref of dir as what a machine wrote
// into it. `suite push` compares the current ref against this one. A match
// declares an eval set nobody read, and the server records that as derived.
//
// Every machine writer of a suite directory must call this, not only the
// generator. A stale ref makes the next push declare nothing. The server then
// records the eval set as authored, and its score can clear a scan hold with
// no reader.
//
// A failure never fails the caller, because the artifacts on disk are good.
// Both failures warn: a silent miss is the laundered suite above.
func recordGeneratedRef(st *store.Store, skill, dir string) {
	ref, err := suite.Ref(dir)
	if err != nil {
		ui.Warn("could not hash the eval set for %s, so the next push declares it authored: %v", skill, err)
		return
	}
	if err := st.RecordGeneratedRef(skill, ref); err != nil {
		ui.Warn("could not record the eval set ref for %s, so the next push declares it authored: %v", skill, err)
	}
}

func init() {
	suiteCmd.AddCommand(suiteGenCmd)
	rootCmd.AddCommand(suiteCmd)
}
