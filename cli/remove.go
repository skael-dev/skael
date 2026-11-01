package cli

import (
	"fmt"
	"os"

	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove <skill-name>",
	Short: "Uninstall a skill",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemove,
}

func init() {
	rootCmd.AddCommand(removeCmd)
}

func runRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	// 1. Load config with migration support.
	dir := config.DefaultDir()
	cfg, _, err := config.EnsureSkillsKey(dir)
	if err != nil {
		return notConfigured(err)
	}

	// 2. Check skill is installed.
	if _, idx := cfg.FindSkill(name); idx < 0 {
		return usageError("not_installed", fmt.Sprintf("skill %q is not installed", name), "skael list --installed")
	}

	// 3. Remove from config.
	cfg.RemoveSkill(name)
	if err := config.WriteConfig(dir, cfg); err != nil {
		return failure("config_error", fmt.Sprintf("write config: %s", err), "")
	}

	// 4. Prune files from disk using state placements.
	state, _ := config.ReadState(dir)
	var updatedSkills []config.SyncedSkill
	for _, s := range state.Skills {
		if s.Name == name {
			for _, p := range s.Placements {
				if err := os.RemoveAll(p.Path); err != nil {
					ui.Warn("prune %s from %s: %s", name, p.Agent, err)
				}
			}
			continue
		}
		updatedSkills = append(updatedSkills, s)
	}
	state.Skills = updatedSkills
	if err := config.WriteState(dir, state); err != nil {
		return failure("state_error", fmt.Sprintf("write state: %s", err), "")
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]string{"status": "removed", "name": name})
	}
	ui.Success("Removed %s", name)
	return nil
}
