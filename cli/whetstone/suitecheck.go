package whetstone

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/ui"
)

// checkRunTimeout bounds one oracle or verifier script. These are reference
// scripts the suite author controls, not an agent session, so a few minutes
// is generous headroom rather than a tight budget.
const checkRunTimeout = 5 * time.Minute

// checkConcurrency bounds how many tasks are checked in parallel. It mirrors
// the default sandbox.Driver resource footprint (see docker.Options), so a
// gate over a large suite does not try to run more containers at once than
// the host can carry.
const checkConcurrency = 4

var suiteCheckAllowVoid bool

var suiteCheckCmd = &cobra.Command{
	Use:   "check <skill>",
	Short: "Gate a skill's suite on its own oracle and verifier",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunSuiteCheck(cmd.Context(), args[0], suiteCheckAllowVoid)
	},
}

// RunSuiteCheck runs the oracle gate over a skill's written suite: for every
// task, does the oracle solve it, does the task's own verifier accept that
// solution, and does the verifier reject an untouched workspace. A task
// failing any of those three is void — excluded from a later eval rather than
// fatal to it, which is what --allow-void is for. Without it, any void task
// makes this command exit non-zero, which is what makes it usable as a CI
// gate: an author cannot otherwise tell a broken oracle from a broken
// verifier without reading source, so the reason each void task carries names
// the check that failed.
func RunSuiteCheck(ctx context.Context, skill string, allowVoid bool) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	sp, err := loadApprovedSpec(st, skill)
	if err != nil {
		return err
	}

	suiteDir, err := st.SuiteDir(skill)
	if err != nil {
		return err
	}
	s, err := suite.Load(suiteDir)
	if err != nil {
		return fmt.Errorf("loading suite for %s: %w (run `whetstone suite gen %s` first)", skill, err, skill)
	}
	suiteRef, err := suite.Ref(suiteDir)
	if err != nil {
		return err
	}

	baseTag := os.Getenv("WHETSTONE_BASE_TAG")
	d, err := docker.New(docker.Options{BaseTag: baseTag, Logger: ui.Info})
	if err != nil {
		return fmt.Errorf("suite check: %w", err)
	}
	if err := d.EnsureBase(ctx, baseTag == imagespec.SlimBaseTag); err != nil {
		return fmt.Errorf("suite check: preparing base image: %w", err)
	}
	image, err := d.Prepare(ctx, sandbox.EnvSpec{Skill: sp.Name, Deps: sp.Deps, BaseTag: baseTag})
	if err != nil {
		return fmt.Errorf("suite check: preparing image: %w", err)
	}

	// suite.Check runs oracle/solve.sh and verifier/test.sh directly, and both
	// are model-generated. Gated is what makes that trust decision structural
	// rather than remembered by this call site: today's whole product is
	// self-hosted, own-team skills, so this — like every other docker-driver
	// caller — passes untrusted: false, matching runner.New's default. A
	// caller that ever needs to run someone else's suite through this path
	// gets refused here instead of silently running it in a shared kernel.
	gd, err := sandbox.Gated(d, false)
	if err != nil {
		return fmt.Errorf("suite check: %w", err)
	}

	results, err := suite.Check(ctx, s, suite.CheckOptions{
		Driver: gd, Image: image, SuiteDir: suiteDir,
		Timeout: checkRunTimeout, Concurrency: checkConcurrency, Logger: ui.Info,
	})
	if err != nil {
		return fmt.Errorf("suite check: %w", err)
	}

	rows := make([]store.SuiteCheckRow, len(results))
	for i, r := range results {
		rows[i] = store.SuiteCheckRow{TaskID: r.TaskID, Void: r.Void, Reason: r.Reason}
	}
	if err := st.SaveSuiteCheck(skill, suiteRef, rows); err != nil {
		return fmt.Errorf("suite check: recording results: %w", err)
	}

	return reportCheckResults(skill, results, allowVoid)
}

// reportCheckResults renders one line per task and returns an error when any
// task is void and allowVoid is false — the exit code that makes `suite
// check` usable as a CI gate.
func reportCheckResults(skill string, results []suite.CheckResult, allowVoid bool) error {
	if ui.JSONMode {
		tasks := make([]map[string]any, len(results))
		for i, r := range results {
			tasks[i] = map[string]any{
				"task_id":            r.TaskID,
				"oracle_exit":        r.OracleExit,
				"verifier_exit":      r.VerifierExit,
				"bare_verifier_exit": r.BareVerifierExit,
				"void":               r.Void,
				"reason":             r.Reason,
			}
		}
		if err := ui.PrintJSON(map[string]any{"skill": skill, "tasks": tasks}); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			if r.Void {
				ui.Warn("%s: void — %s", r.TaskID, r.Reason)
			} else {
				ui.Success("%s: oracle and verifier agree", r.TaskID)
			}
		}
	}

	voidSet := suite.VoidSet(results)
	if len(voidSet) == 0 {
		if !ui.JSONMode {
			ui.Success("%d tasks checked, none void", len(results))
		}
		return nil
	}
	if !ui.JSONMode {
		ui.Warn("%d of %d tasks void", len(voidSet), len(results))
	}
	if allowVoid {
		return nil
	}
	return fmt.Errorf("%d of %d tasks void; fix them, or pass --allow-void to exclude them from the eval", len(voidSet), len(results))
}

func init() {
	suiteCheckCmd.Flags().BoolVar(&suiteCheckAllowVoid, "allow-void", false,
		"Exit 0 even if some tasks are void; they are still excluded from a later eval")
	suiteCmd.AddCommand(suiteCheckCmd)
}
