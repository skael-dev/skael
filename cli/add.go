package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add <skill-name>",
	Short: "Install a skill from the registry",
	Args:  cobra.ExactArgs(1),
	RunE:  runAdd,
}

var addScope string

func init() {
	addCmd.Flags().StringVar(&addScope, "scope", "", "Skill placement scope: project|user (overrides default)")
	rootCmd.AddCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	if addScope != "" && !validScope(addScope) {
		ui.Errorf("invalid --scope %q: must be \"project\" or \"user\"", addScope)
		return fmt.Errorf("invalid scope")
	}

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

	// 2. Resolve the effective scope for this skill.
	effectiveScope := resolveScope(addScope, cfg.Scope)

	// 3. Check if already installed with the same scope.
	if existing, idx := cfg.FindSkill(name); idx >= 0 {
		existingScope := existing.Scope
		if existingScope == "" {
			existingScope = string(resolveScope("", cfg.Scope))
		}
		if addScope == "" || addScope == existingScope {
			if ui.JSONMode {
				return ui.PrintJSON(map[string]string{"status": "already_installed", "name": name, "scope": existingScope})
			}
			ui.Info("Skill %q is already installed (%s scope).", name, existingScope)
			return nil
		}
	}

	// 4. Validate skill exists on the registry.
	sp := StartSpinner("Checking registry...")
	c := client.New(cfg.Endpoint, cfg.APIKey)
	manifest, err := c.GetManifest()
	if err != nil {
		sp.Stop()
		ui.Errorf("fetch manifest: %s", err)
		return nil
	}

	var found *client.ManifestEntry
	for i, entry := range manifest {
		if entry.Name == name {
			found = &manifest[i]
			break
		}
	}
	if found == nil {
		sp.Stop()
		if ui.JSONMode {
			ui.PrintJSONError(fmt.Sprintf("skill %q not found on registry", name), "not_found", "skael search <term>")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    fmt.Sprintf("Skill %q not found on the registry", name),
			Suggestion: "skael search <term>",
		})
		return nil
	}

	// 5. Add to config.
	cfg.AddSkill(name, string(effectiveScope))
	if err := config.WriteConfig(dir, cfg); err != nil {
		sp.Stop()
		ui.Errorf("write config: %s", err)
		return fmt.Errorf("write config: %w", err)
	}

	// 6. Download and extract.
	sp.Update(fmt.Sprintf("Downloading %s v%d...", name, found.Version))
	archive, err := c.DownloadVersion(name, found.Version)
	if err != nil {
		sp.Stop()
		ui.Errorf("download %s: %s", name, err)
		return nil
	}

	actualChecksum := fmt.Sprintf("%x", sha256.Sum256(archive))
	if found.Checksum != "" && actualChecksum != found.Checksum {
		sp.Stop()
		ui.Errorf("checksum mismatch for %s (expected %s, got %s)", name, found.Checksum[:16], actualChecksum[:16])
		return nil
	}

	// 7. Detect agents and extract.
	home, err := os.UserHomeDir()
	if err != nil {
		sp.Stop()
		ui.Errorf("cannot determine home directory: %s", err)
		return nil
	}

	var projectRoot string
	if effectiveScope == ScopeProject {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			sp.Stop()
			ui.Errorf("cannot determine working directory: %s", wdErr)
			return nil
		}
		projectRoot = gitRoot(wd)
	}

	detectedAgents := agents.DetectIn(home)
	var placements []config.Placement
	for _, agent := range detectedAgents {
		destDir := filepath.Join(agentSkillsBase(agent, effectiveScope, home, projectRoot), name)
		if err := extractSkillAtomically(archive, destDir); err != nil {
			ui.Errorf("extract %s to %s: %s", name, agent.Name(), err)
		} else {
			placements = append(placements, config.Placement{
				Agent: agent.Name(),
				Path:  destDir,
				Scope: string(effectiveScope),
			})
		}
	}

	// 8. Update state.
	state, _ := config.ReadState(dir)
	var updatedSkills []config.SyncedSkill
	for _, s := range state.Skills {
		if s.Name != name {
			updatedSkills = append(updatedSkills, s)
		}
	}
	updatedSkills = append(updatedSkills, config.SyncedSkill{
		Name:       name,
		Version:    found.Version,
		Checksum:   found.Checksum,
		Placements: placements,
	})
	state.Skills = updatedSkills
	if err := config.WriteState(dir, state); err != nil {
		sp.Stop()
		ui.Errorf("write state: %s", err)
		return fmt.Errorf("write state: %w", err)
	}

	sp.Stop()
	if ui.JSONMode {
		return ui.PrintJSON(map[string]interface{}{
			"status":  "added",
			"name":    name,
			"version": found.Version,
			"scope":   string(effectiveScope),
		})
	}
	ui.Success("Added %s v%d (%s scope)", name, found.Version, effectiveScope)
	return nil
}
