package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

// extractSkillAtomically unpacks archive into destDir using a temp-dir + rename
// swap so that a mid-extraction failure (disk full, bad entry, etc.) never
// leaves a partial or empty skill directory that the agent would load.
//
// Strategy:
//  1. Unpack to a sibling temp dir (same parent ⇒ same filesystem → rename is
//     a single metadata operation).
//  2. On unpack failure: remove the temp dir and return the error; destDir is
//     untouched.
//  3. On success: remove the old destDir, then rename the temp dir into place.
//     The failure window shrinks to the gap between RemoveAll and Rename — two
//     fast syscalls — rather than spanning the entire extraction.
func extractSkillAtomically(archive []byte, destDir string) error {
	parent := filepath.Dir(destDir)
	base := filepath.Base(destDir)

	// Create the temp dir in the same parent so the rename stays on-filesystem.
	tmpDir, err := os.MkdirTemp(parent, base+fmt.Sprintf(".tmp-%d-*", rand.Int63()))
	if err != nil {
		return fmt.Errorf("extractSkillAtomically: create temp dir: %w", err)
	}

	if err := skill.Unpack(bytes.NewReader(archive), tmpDir); err != nil {
		// Extraction failed — clean up the temp dir and leave destDir intact.
		_ = os.RemoveAll(tmpDir)
		return err
	}

	// Extraction succeeded — swap in the new content atomically.
	if err := os.RemoveAll(destDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("extractSkillAtomically: remove old destDir: %w", err)
	}
	if err := os.Rename(tmpDir, destDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("extractSkillAtomically: rename temp dir to destDir: %w", err)
	}
	return nil
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync skills from the platform to local agent directories",
	RunE:  runSync,
}

var (
	syncDryRun bool
	syncAgent  string
	syncQuiet  bool
	syncScope  string
)

func init() {
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Show what would happen")
	syncCmd.Flags().StringVar(&syncAgent, "agent", "", "Sync only for this agent")
	syncCmd.Flags().BoolVar(&syncQuiet, "quiet", false, "Suppress non-error output")
	syncCmd.Flags().StringVar(&syncScope, "scope", "", "Skill placement scope: project|user (default: config or project)")
	rootCmd.AddCommand(syncCmd)
}

// runSync is a package-level function so setup.go (Task 10) can call it directly.
func runSync(cmd *cobra.Command, args []string) error {
	// 1. Load config with skills-key migration.
	dir := config.DefaultDir()
	cfg, migrated, err := config.EnsureSkillsKey(dir)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("not configured", "not_configured", "skael setup <url> <api-key>")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "not configured",
			Suggestion: "skael setup <url> <api-key>",
		})
		return nil
	}

	// Print migration message if applicable.
	if migrated != nil && !syncQuiet && !ui.JSONMode {
		if len(migrated) > 0 {
			ui.Success("Migrated to selective sync. Added %d %s from your current state:",
				len(migrated), plural(len(migrated), "skill", "skills"))
			for _, name := range migrated {
				entry, _ := cfg.FindSkill(name)
				scopeLabel := entry.Scope
				if scopeLabel == "" {
					scopeLabel = cfg.Scope
					if scopeLabel == "" {
						scopeLabel = "project"
					}
				}
				fmt.Fprintf(os.Stderr, "  %s (%s)\n", name, scopeLabel)
			}
			ui.Info("Run \"skael add/remove <name>\" to manage your skill list.")
		} else {
			ui.Info("No skills installed. Run \"skael add <name>\" to get started.")
		}
	}

	// Check for empty skills list (no-op sync).
	if len(cfg.Skills) == 0 {
		if ui.JSONMode {
			out := struct {
				Synced int      `json:"synced"`
				Failed int      `json:"failed"`
				Pruned int      `json:"pruned"`
				Agents []string `json:"agents"`
				Total  int      `json:"total"`
			}{Agents: []string{}}
			return ui.PrintJSON(out)
		}
		if !syncQuiet {
			ui.Info("No skills installed. Run \"skael add <name>\" to get started.")
		}
		return nil
	}

	// 1b. Resolve placement scope (flag > config > project), detect agents,
	// and resolve the project root if needed. Done early so --dry-run can
	// report destinations too.
	if syncScope != "" && !validScope(syncScope) {
		ui.Errorf("invalid --scope %q: must be \"project\" or \"user\"", syncScope)
		return fmt.Errorf("invalid scope")
	}
	scope := resolveScope(syncScope, cfg.Scope)

	home, err := os.UserHomeDir()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("cannot determine home directory", "home_error", "")
			return nil
		}
		ui.Errorf("cannot determine home directory: %s", err)
		return nil
	}

	// Resolve projectRoot if the global scope is project OR any per-skill scope is project.
	var projectRoot string
	needsProjectRoot := scope == ScopeProject
	if !needsProjectRoot {
		for _, s := range cfg.Skills {
			if resolveScope(s.Scope, cfg.Scope) == ScopeProject {
				needsProjectRoot = true
				break
			}
		}
	}
	if needsProjectRoot {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			ui.Errorf("cannot determine working directory: %s", wdErr)
			return wdErr
		}
		projectRoot = gitRoot(wd)
	}

	detectedAgents := agents.DetectIn(home)
	if syncAgent != "" {
		var filtered []agents.Agent
		for _, a := range detectedAgents {
			if a.Name() == syncAgent {
				filtered = append(filtered, a)
			}
		}
		if len(filtered) == 0 {
			ui.Errorf("agent %q not detected", syncAgent)
			return nil
		}
		detectedAgents = filtered
	}

	// 2. Create client and get manifest.
	sp := StartSpinner("Fetching manifest...")
	c := client.New(cfg.Endpoint, cfg.APIKey)
	manifest, err := c.GetManifest()
	if err != nil {
		sp.Stop()
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	// 2b. Filter manifest to only skills in config.
	wantedSet := make(map[string]struct{}, len(cfg.Skills))
	for _, s := range cfg.Skills {
		wantedSet[s.Name] = struct{}{}
	}

	var filtered []client.ManifestEntry
	for _, entry := range manifest {
		if _, wanted := wantedSet[entry.Name]; wanted {
			filtered = append(filtered, entry)
		}
	}

	// Warn about skills in config that aren't in the manifest.
	manifestSet := make(map[string]struct{}, len(manifest))
	for _, entry := range manifest {
		manifestSet[entry.Name] = struct{}{}
	}
	for _, s := range cfg.Skills {
		if _, inManifest := manifestSet[s.Name]; !inManifest {
			if !syncQuiet {
				ui.Warn("skill %q not found on registry — skipping", s.Name)
			}
		}
	}

	// 3. Read local state.
	state, err := config.ReadState(dir)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "state_error", "")
			return nil
		}
		ui.Errorf("read state: %s", err)
		return nil
	}

	// 4. Build local map: name → SyncedSkill.
	localMap := make(map[string]config.SyncedSkill, len(state.Skills))
	for _, s := range state.Skills {
		localMap[s.Name] = s
	}

	// 5. Compute diff against filtered manifest (only wanted skills).
	type toSync struct {
		entry client.ManifestEntry
		isNew bool
	}
	var pending []toSync

	for _, entry := range filtered {
		local, exists := localMap[entry.Name]
		if !exists {
			pending = append(pending, toSync{entry: entry, isNew: true})
		} else if entry.Version > local.Version || entry.Checksum != local.Checksum {
			pending = append(pending, toSync{entry: entry, isNew: false})
		}
	}

	// 5b. Check if any local skills need pruning (not in config).
	hasPrunable := false
	for _, s := range state.Skills {
		if _, inConfig := wantedSet[s.Name]; !inConfig && len(s.Placements) > 0 {
			hasPrunable = true
			break
		}
	}

	// 6. If no changes and nothing to prune, print up-to-date and summary.
	if len(pending) == 0 && !hasPrunable {
		sp.Stop()
		if ui.JSONMode {
			out := struct {
				Synced int      `json:"synced"`
				Failed int      `json:"failed"`
				Pruned int      `json:"pruned"`
				Agents []string `json:"agents"`
				Total  int      `json:"total"`
			}{
				Synced: 0,
				Failed: 0,
				Pruned: 0,
				Agents: []string{},
				Total:  len(cfg.Skills),
			}
			return ui.PrintJSON(out)
		}
		if !syncQuiet {
			ui.Success("Already up to date")
			ui.Summary(
				fmt.Sprintf("0 updated"),
				fmt.Sprintf("0 failed"),
				fmt.Sprintf("0 removed"),
				fmt.Sprintf("%d total", len(cfg.Skills)),
			)
		}
		return nil
	}

	// 7. If --dry-run, show what would happen and return.
	sp.Stop()
	if syncDryRun {
		if !syncQuiet {
			ui.Info("scope: %s", scope)
			for _, agent := range detectedAgents {
				ui.Info("  %s → %s", agent.Name(), agentSkillsBase(agent, scope, home, projectRoot))
			}
			for _, ts := range pending {
				ver := fmt.Sprintf("v%d", ts.entry.Version)
				if ts.isNew {
					ui.New(ts.entry.Name, ver)
				} else {
					ui.Download(ts.entry.Name, ver)
				}
			}
			ui.Summary(
				fmt.Sprintf("%d to sync", len(pending)),
				fmt.Sprintf("%d total", len(cfg.Skills)),
			)
		}
		return nil
	}

	// 9. For each skill to sync: download and extract.
	type syncResult struct {
		name    string
		version int
		failed  bool
	}
	var results []syncResult
	var newSkills []config.SyncedSkill

	// Carry over skills that didn't need updating and are still in config.
	// Skills not in config are handled by the prune step below.
	for name, local := range localMap {
		if _, inConfig := wantedSet[name]; !inConfig {
			continue
		}
		needsUpdate := false
		for _, ts := range pending {
			if ts.entry.Name == name {
				needsUpdate = true
				break
			}
		}
		if !needsUpdate {
			newSkills = append(newSkills, local)
		}
	}

	for i, ts := range pending {
		sp.Update(fmt.Sprintf("Downloading %s (%d/%d)...", ts.entry.Name, i+1, len(pending)))
		archive, dlErr := c.DownloadVersion(ts.entry.Name, ts.entry.Version)
		if dlErr != nil {
			ui.Errorf("download %s v%d: %s", ts.entry.Name, ts.entry.Version, dlErr)
			results = append(results, syncResult{name: ts.entry.Name, version: ts.entry.Version, failed: true})
			continue
		}

		// Verify checksum against manifest entry.
		actualChecksum := fmt.Sprintf("%x", sha256.Sum256(archive))
		if ts.entry.Checksum != "" && actualChecksum != ts.entry.Checksum {
			ui.Warn("checksum mismatch for %s (expected %s, got %s)", ts.entry.Name, ts.entry.Checksum[:16], actualChecksum[:16])
			results = append(results, syncResult{name: ts.entry.Name, version: ts.entry.Version, failed: true})
			continue
		}

		// Resolve per-skill scope: skill config scope > flag > global config > default.
		skillScope := scope
		if entry, idx := cfg.FindSkill(ts.entry.Name); idx >= 0 && entry.Scope != "" {
			skillScope = Scope(entry.Scope)
		}

		// Extract to each detected agent's skills directory.
		// Track per-agent success so a partial failure doesn't corrupt state.
		// extractSkillAtomically unpacks to a sibling temp dir first, so a
		// mid-extraction failure leaves the previous skill version intact.
		extractOK := 0
		extractFail := 0
		var placements []config.Placement
		for _, agent := range detectedAgents {
			destDir := filepath.Join(agentSkillsBase(agent, skillScope, home, projectRoot), ts.entry.Name)
			if err := extractSkillAtomically(archive, destDir); err != nil {
				ui.Errorf("extract %s to %s: %s", ts.entry.Name, agent.Name(), err)
				extractFail++
			} else {
				extractOK++
				placements = append(placements, config.Placement{
					Agent: agent.Name(),
					Path:  destDir,
					Scope: string(skillScope),
				})
			}
		}

		ver := fmt.Sprintf("v%d", ts.entry.Version)
		if extractOK == 0 && (extractFail > 0 || len(detectedAgents) == 0) {
			// All agents failed (or no agents); mark as failed and don't record.
			results = append(results, syncResult{name: ts.entry.Name, version: ts.entry.Version, failed: true})
		} else {
			// At least one agent succeeded; record the skill and warn about failures.
			if extractFail > 0 {
				ui.Errorf("extract %s: succeeded for %d agent(s), failed for %d agent(s)", ts.entry.Name, extractOK, extractFail)
			}
			if !syncQuiet {
				if ts.isNew {
					ui.New(ts.entry.Name, ver)
				} else {
					ui.Download(ts.entry.Name, ver)
				}
			}
			results = append(results, syncResult{name: ts.entry.Name, version: ts.entry.Version, failed: false})
			newSkills = append(newSkills, config.SyncedSkill{
				Name:       ts.entry.Name,
				Version:    ts.entry.Version,
				Checksum:   ts.entry.Checksum,
				Placements: placements,
			})
		}
	}

	// 9b. Prune skills that exist in state but are no longer in config.
	pruned := 0
	for _, local := range state.Skills {
		if _, inConfig := wantedSet[local.Name]; inConfig {
			continue
		}
		if len(local.Placements) == 0 {
			if !syncQuiet {
				ui.Info("skip prune %s: no recorded placements (old state format)", local.Name)
			}
			newSkills = append(newSkills, local)
			continue
		}
		if syncDryRun {
			if !syncQuiet {
				for _, p := range local.Placements {
					ui.Warn("would remove %s from %s (%s)", local.Name, p.Agent, p.Path)
				}
			}
			newSkills = append(newSkills, local)
			continue
		}
		sp.Update(fmt.Sprintf("Removing %s...", local.Name))
		for _, p := range local.Placements {
			if err := os.RemoveAll(p.Path); err != nil {
				ui.Errorf("prune %s from %s: %s", local.Name, p.Agent, err)
			} else if !syncQuiet {
				ui.Warn("removed %s from %s (%s)", local.Name, p.Agent, p.Path)
			}
		}
		pruned++
	}

	sp.Stop()

	// 10. Write new state file.
	newState := &config.SyncState{
		LastSync: time.Now().UTC().Format(time.RFC3339),
		Skills:   newSkills,
	}
	if err := config.WriteState(dir, newState); err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(fmt.Sprintf("write state: %s", err), "state_error", "")
			return fmt.Errorf("write state: %w", err)
		}
		ui.Errorf("write state: %s", err)
		return fmt.Errorf("write state: %w", err)
	}

	// 11. Print summary.
	synced := 0
	failed := 0
	for _, r := range results {
		if r.failed {
			failed++
		} else {
			synced++
		}
	}

	agentNames := make([]string, 0, len(detectedAgents))
	dests := make(map[string]string, len(detectedAgents))
	for _, a := range detectedAgents {
		agentNames = append(agentNames, a.Name())
		dests[a.Name()] = agentSkillsBase(a, scope, home, projectRoot)
	}

	// 12. If JSONMode: print JSON.
	if ui.JSONMode {
		out := struct {
			Synced int               `json:"synced"`
			Failed int               `json:"failed"`
			Pruned int               `json:"pruned"`
			Agents []string          `json:"agents"`
			Scope  string            `json:"scope"`
			Dests  map[string]string `json:"dests"`
			Total  int               `json:"total"`
		}{
			Synced: synced,
			Failed: failed,
			Pruned: pruned,
			Agents: agentNames,
			Scope:  string(scope),
			Dests:  dests,
			Total:  len(cfg.Skills),
		}
		return ui.PrintJSON(out)
	}

	if !syncQuiet {
		parts := []string{
			fmt.Sprintf("%d updated", synced),
			fmt.Sprintf("%d failed", failed),
			fmt.Sprintf("%d removed", pruned),
			fmt.Sprintf("%d total", len(cfg.Skills)),
		}
		if len(agentNames) > 0 {
			parts = append(parts, strings.Join(agentNames, ", "))
		}
		ui.Summary(parts...)
		// Report the concrete destination per agent so placement is never a surprise.
		for _, a := range detectedAgents {
			ui.Info("  %s → %s · %s", a.Name(), scope, dests[a.Name()])
		}
	}

	return nil
}
