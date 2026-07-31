package whetstone

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/ui"
)

var newYes bool

var newCmd = &cobra.Command{
	Use:   "new <intent>",
	Short: "Interview, generate, lint, and evaluate a new skill",
	Long: "Draft a specification from a plain-language intent, store it, ask you to\n" +
		"approve it, then generate the bundle, lint it, compile its drift contract,\n" +
		"and draft its evaluation suite.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunNew(cmd.Context(), args[0], newYes)
	},
}

// RunNew runs the full authoring pipeline for a new skill against the
// workspace and gateway this machine is configured for.
func RunNew(ctx context.Context, intent string, yes bool) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	g, err := newGateway(st.Cache())
	if err != nil {
		return err
	}
	return runNew(ctx, st, g, os.Stdin, intent, yes)
}

// runNew is the pipeline itself, with the store, the gateway, and the
// approval gate's input passed in. RunNew is the thin wrapper that resolves
// those from the environment; everything worth testing — the approval gate's
// parsing, and stopping before the contract when the bundle fails lint — is
// here, where a fake gateway and a string reader can reach it.
func runNew(ctx context.Context, st *store.Store, g llm.Gateway, in io.Reader, intent string, yes bool) error {
	ui.Info("drafting a specification…")
	sp, err := spec.Interview(ctx, g, intent)
	if err != nil {
		return err
	}

	version, err := st.SaveSpec(sp)
	if err != nil {
		return err
	}
	ui.Success("stored %s spec version %d", sp.Name, version)

	if !yes {
		approved, err := confirmSpec(sp, in)
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("spec version %d for %s was not approved; edit it with `whetstone spec edit %s`",
				version, sp.Name, sp.Name)
		}
	}
	if err := st.ApproveSpec(sp.Name, version); err != nil {
		return err
	}

	bundle, err := generateBundle(ctx, st, g, sp)
	if err != nil {
		return err
	}

	res, code, err := lintBundle(bundle.Dir, false)
	if err != nil {
		return err
	}
	renderFindings(res)
	if code != 0 {
		// Stopping here is deliberate. A contract compiled from a spec whose
		// bundle does not lint describes a skill that does not exist, and a
		// suite drafted against it measures nothing.
		return fmt.Errorf("the generated bundle at %s does not lint clean (%s); fix it and re-run `whetstone gen %s`",
			bundle.Dir, plural(res.Errors(), "error"), sp.Name)
	}

	if err := writeContract(st, sp); err != nil {
		return err
	}
	return generateSuite(ctx, st, g, sp)
}

// writeContract compiles the drift contract from the spec and writes it into
// the skill's eval sidecar.
func writeContract(st *store.Store, sp *spec.SkillSpec) error {
	c, err := contract.Compile(sp)
	if err != nil {
		return err
	}

	path, err := st.ContractPath(sp.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("whetstone new: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("whetstone new: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := c.Save(f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("whetstone new: writing %s: %w", path, err)
	}
	ui.Success("compiled contract to %s", path)
	return nil
}

// confirmSpec prints the drafted spec and asks for approval. It is the human
// gate: everything downstream — the bundle, the contract, the suite — is
// derived from this document, so it is the only place review is cheap.
func confirmSpec(sp *spec.SkillSpec, in io.Reader) (bool, error) {
	if err := sp.Save(os.Stdout); err != nil {
		return false, err
	}
	fmt.Fprintf(os.Stderr, "\n  Approve this spec for %s? [y/N] ", sp.Name)

	// EOF is a decline, not a failure. The prompt is "[y/N]", so a closed or
	// empty stdin — `whetstone new … < /dev/null`, or any non-interactive
	// runner — means no consent was given, which is exactly what N means.
	// Reporting it as an error instead would turn "the gate said no" into "the
	// gate broke", and the caller cannot tell those apart.
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("whetstone new: reading approval: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func init() {
	newCmd.Flags().BoolVar(&newYes, "yes", false, "Skip the spec approval prompt")
	rootCmd.AddCommand(newCmd)
}
