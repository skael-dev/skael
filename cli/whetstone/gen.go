package whetstone

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/ui"
)

var genCmd = &cobra.Command{
	Use:   "gen <skill>",
	Short: "Regenerate a skill bundle from its approved spec",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunGen(cmd.Context(), args[0])
	},
}

// RunGen regenerates the bundle for a skill from its approved spec and lints
// the result.
func RunGen(ctx context.Context, skill string) error {
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
		return fmt.Errorf("the generated bundle at %s does not lint clean: %s", bundle.Dir, plural(res.Errors(), "error"))
	}
	return nil
}

// generateBundle writes the bundle into the workspace's skills directory.
// gen.Generate appends the spec's own directory name to outDir, so outDir is
// the parent of the skill directory rather than the skill directory itself —
// derived from the store's own helper so the two cannot disagree.
func generateBundle(ctx context.Context, st *store.Store, g llm.Gateway, sp *spec.SkillSpec) (*gen.Bundle, error) {
	skillDir, err := st.SkillDir(sp.Name)
	if err != nil {
		return nil, err
	}

	bundle, err := gen.Generate(ctx, g, sp, filepath.Dir(skillDir))
	if err != nil {
		return nil, err
	}
	ui.Success("generated %d files in %s", len(bundle.Files), bundle.Dir)
	return bundle, nil
}

func init() {
	rootCmd.AddCommand(genCmd)
}
