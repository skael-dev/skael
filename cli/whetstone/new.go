package whetstone

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/ui"
)

var newCmd = &cobra.Command{
	Use:   "new <intent>",
	Short: "Interview, generate, lint, and draft the eval set for a new skill",
	Long: "Draft a specification from a plain-language intent, store it, generate\n" +
		"the bundle, lint it, and draft its evaluation set. Every run ends with\n" +
		"SKILL.md, evals/evals.json, and evals/triggers.json.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunNew(cmd.Context(), args[0])
	},
}

// RunNew runs the full authoring pipeline for a new skill against the
// workspace and gateway this machine is configured for.
func RunNew(ctx context.Context, intent string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	g, err := newGateway(st.Cache())
	if err != nil {
		return err
	}
	return runNew(ctx, st, g, intent)
}

// runNew is the pipeline itself, with the store and the gateway passed in.
// It never asks a question. A person who wants to change the drafted spec
// edits it afterwards. That is why the run prints the spec and names the
// next commands at the end.
func runNew(ctx context.Context, st *store.Store, g llm.Gateway, intent string) error {
	ui.Info("drafting a specification…")
	sp, err := spec.Interview(ctx, g, intent)
	if err != nil {
		return err
	}

	version, err := st.SaveSpec(sp)
	if err != nil {
		return err
	}
	// Every ui writer no-ops in JSON mode to keep stdout parseable. This
	// writes YAML straight to stdout, so it must obey the same rule.
	if !ui.JSONMode {
		if err := sp.Save(os.Stdout); err != nil {
			return err
		}
	}
	ui.Success("stored %s spec version %d", sp.Name, version)

	if err := st.ApproveSpec(sp.Name, version); err != nil {
		return err
	}

	// The eval set is drafted beside the bundle: suite.Generate reads the
	// approved spec and writes to the suite directory, so it depends on
	// nothing the bundle's lint gate protects.
	suiteErrCh := make(chan error, 1)
	go func() { suiteErrCh <- generateSuite(ctx, st, g, sp) }()

	bundle, bundleErr := generateBundle(ctx, st, g, sp)
	suiteErr := <-suiteErrCh
	if bundleErr != nil {
		if suiteErr != nil {
			ui.Warn("the eval set also failed: %v", suiteErr)
		}
		return wrapGenerationError(bundleErr, "whetstone gen "+sp.Name)
	}

	res, code, err := lintBundle(bundle.Dir, false)
	if err != nil {
		return err
	}
	renderFindings(res)
	if code != 0 {
		// The lint gate still fails the run. The drafted eval set is kept,
		// because it is written before the gate reads the bundle, and a
		// person fixing the bundle needs it.
		if suiteErr != nil {
			ui.Warn("the eval set also failed: %v", suiteErr)
		}
		return fmt.Errorf("the generated bundle at %s does not lint clean (%s); fix it and re-run `whetstone gen %s`",
			bundle.Dir, plural(res.Errors(), "error"), sp.Name)
	}

	if suiteErr != nil {
		return wrapGenerationError(suiteErr, "whetstone suite gen "+sp.Name)
	}

	ui.Info("edit the skill with %s then %s", ui.Code("whetstone spec edit "+sp.Name), ui.Code("whetstone gen "+sp.Name))
	ui.Info("score it with %s", ui.Code("whetstone eval "+sp.Name))
	return nil
}

// wrapGenerationError appends a resume hint to a generation-pass failure.
// Every completed pass is cached (internal/eval/store), so a failure partway
// through a multi-call pipeline does not have to restart from the interview —
// resumeCmd is the command that picks up where it left off. A timeout gets
// the WHETSTONE_LLM_TIMEOUT hint too, since that is the direct remedy for it
// rather than something the operator has to already know to look for.
func wrapGenerationError(err error, resumeCmd string) error {
	hint := fmt.Sprintf("completed passes are cached — resume with %s", ui.Code(resumeCmd))
	if errors.Is(err, llm.ErrTimeout) {
		hint += fmt.Sprintf("; or raise the timeout with %s", ui.Code(timeoutEnv+"=<duration>"))
	}
	return fmt.Errorf("%w; %s", err, hint)
}

// newYes is read by nothing. `whetstone new` used to ask for the spec to be
// approved, and --yes skipped that question. The question is gone. The flag
// stays so a script that still passes it does not fail on an unknown flag.
var newYes bool

func init() {
	newCmd.Flags().BoolVar(&newYes, "yes", false, "Deprecated: does nothing")
	if err := newCmd.Flags().MarkDeprecated("yes", "the approval prompt was removed, so this flag does nothing"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(newCmd)
}
