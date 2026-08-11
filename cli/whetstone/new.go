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
// edits it afterwards, which is why the spec is printed and the next commands
// are named at the end.
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
	if err := sp.Save(os.Stdout); err != nil {
		return err
	}
	ui.Success("stored %s spec version %d", sp.Name, version)

	if err := st.ApproveSpec(sp.Name, version); err != nil {
		return err
	}

	bundle, err := generateBundle(ctx, st, g, sp)
	if err != nil {
		return wrapGenerationError(err, "whetstone gen "+sp.Name)
	}

	res, code, err := lintBundle(bundle.Dir, false)
	if err != nil {
		return err
	}
	renderFindings(res)
	if code != 0 {
		// Stopping here is deliberate. An eval set drafted against a bundle
		// that does not lint measures a skill that does not exist.
		return fmt.Errorf("the generated bundle at %s does not lint clean (%s); fix it and re-run `whetstone gen %s`",
			bundle.Dir, plural(res.Errors(), "error"), sp.Name)
	}

	if err := generateSuite(ctx, st, g, sp); err != nil {
		return wrapGenerationError(err, "whetstone suite gen "+sp.Name)
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

func init() {
	rootCmd.AddCommand(newCmd)
}
