package whetstone

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/ui"
)

var suiteCheckAllowVoid bool

var suiteCheckCmd = &cobra.Command{
	Use:   "check <skill>",
	Short: "Check that every eval can be run and scored",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunSuiteCheck(args[0], suiteCheckAllowVoid)
	},
}

// RunSuiteCheck validates a skill's eval set and records the result.
//
// It replaces an oracle gate that ran every task's reference solution in a
// container. With no verifier script left to prove correct, what remains is
// static: an eval with nothing to grade, or one naming an input file the set
// does not carry, cannot produce a measurement. Both are recorded as void so
// the eval that runs later knows what to exclude.
//
// A void eval makes this exit non-zero unless allowVoid is set, which is what
// makes the command usable as a CI gate.
func RunSuiteCheck(skill string, allowVoid bool) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	suiteDir, err := st.SuiteDir(skill)
	if err != nil {
		return err
	}
	set, err := suite.LoadEvalSet(suiteDir)
	if err != nil {
		return fmt.Errorf("loading the eval set for %s: %w (run `whetstone suite gen %s` first)", skill, err, skill)
	}
	suiteRef, err := suite.Ref(suiteDir)
	if err != nil {
		return err
	}

	results := suite.Validate(suiteDir, set)

	rows := make([]store.SuiteCheckRow, len(results))
	for i, r := range results {
		rows[i] = store.SuiteCheckRow{TaskID: strconv.Itoa(r.ID), Void: r.Void, Reason: r.Reason}
	}
	if err := st.SaveSuiteCheck(skill, suiteRef, rows); err != nil {
		return fmt.Errorf("suite check: recording results: %w", err)
	}

	return reportCheckResults(skill, results, allowVoid)
}

// reportCheckResults renders one line per eval and returns an error when any
// eval is void and allowVoid is false.
func reportCheckResults(skill string, results []suite.EvalCheck, allowVoid bool) error {
	voidCount := 0
	for _, r := range results {
		if r.Void {
			voidCount++
		}
	}

	if ui.JSONMode {
		evals := make([]map[string]any, len(results))
		for i, r := range results {
			evals[i] = map[string]any{"id": r.ID, "void": r.Void, "reason": r.Reason}
		}
		if err := ui.PrintJSON(map[string]any{
			"skill": skill, "evals": evals, "void": voidCount,
		}); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			if r.Void {
				ui.Warn("eval %d: %s", r.ID, r.Reason)
			} else {
				ui.Success("eval %d", r.ID)
			}
		}
	}

	if voidCount > 0 && !allowVoid {
		return fmt.Errorf("suite check: %d of %d evals cannot be scored for %s; fix them, or pass --allow-void to exclude them",
			voidCount, len(results), skill)
	}
	return nil
}

func init() {
	suiteCheckCmd.Flags().BoolVar(&suiteCheckAllowVoid, "allow-void", false, "Exit zero even when some evals cannot be scored")
	suiteCmd.AddCommand(suiteCheckCmd)
}
