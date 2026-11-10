package cli

import (
	"fmt"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove <skill | pattern> | --set <name>",
	Short: "Stop following a skill, a pattern or a set",
	Long: `Stop following a skill, a pattern or a set, and remove the skills no other
followed source still wants. A skill you also follow by name, or through
another pattern or set, stays.`,
	Example: `  skael remove code-review
  skael remove 'payments:*'
  skael remove --set payments`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRemove,
}

var removeSet string

func init() {
	removeCmd.Flags().StringVar(&removeSet, "set", "", "Stop following a set")
	rootCmd.AddCommand(removeCmd)
}

func runRemove(cmd *cobra.Command, args []string) error {
	arg := ""
	if len(args) == 1 {
		arg = args[0]
	}
	src, err := sourceOf(arg, removeSet, "")
	if err != nil {
		return usageError("invalid_source", err.Error(), "skael remove --help")
	}
	dir := config.DefaultDir()
	cfg, _, err := config.EnsureSkillsKey(dir)
	if err != nil {
		return notConfigured(err)
	}
	label := sourceLabel(src)
	if !cfg.RemoveSource(src.Kind, src.Name) {
		return usageError("not_following", fmt.Sprintf("not following %s", label), "skael list --installed")
	}
	if err := config.WriteConfig(dir, cfg); err != nil {
		return failure("config_error", fmt.Sprintf("write config: %s", err), "")
	}
	res, err := reconcile(client.New(cfg.Endpoint, cfg.APIKey), cfg, dir, reconcileOptions{Quiet: true})
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"status": "removed", "source": label, "removed": res.Pruned})
	}
	ui.Success("Stopped following %s; removed %d %s", label, res.Pruned, plural(res.Pruned, "skill", "skills"))
	return nil
}
