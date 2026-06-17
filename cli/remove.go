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
		ui.Error(ui.ErrorDetail{
			Message:    "not configured",
			Suggestion: "skael setup <url> <api-key>",
		})
		return nil
	}

	// 2. Check skill is installed.
	if _, idx := cfg.FindSkill(name); idx < 0 {
		if ui.JSONMode {
			ui.PrintJSONError(fmt.Sprintf("skill %q is not installed", name), "not_installed", "skael list --installed")
			return nil
		}
		ui.Errorf("Skill %q is not installed.", name)
		return nil
	}

	// 3. Remove from config.
	cfg.RemoveSkill(name)
	if err := config.WriteConfig(dir, cfg); err != nil {
		ui.Errorf("write config: %s", err)
		return fmt.Errorf("write config: %w", err)
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
		ui.Errorf("write state: %s", err)
		return fmt.Errorf("write state: %w", err)
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]string{"status": "removed", "name": name})
	}
	ui.Success("Removed %s", name)
	return nil
}
