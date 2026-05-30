package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
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
	// 1. Load config.
	cfg, err := config.LoadConfig()
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

	var projectRoot string
	if scope == ScopeProject {
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
	c := client.New(cfg.Endpoint, cfg.APIKey)
	manifest, err := c.GetManifest()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	// 3. Read local state.
	dir := config.DefaultDir()
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

	// 5. Compute diff.
	type toSync struct {
		entry   client.ManifestEntry
		isNew   bool
	}
	var pending []toSync

	for _, entry := range manifest {
		local, exists := localMap[entry.Name]
		if !exists {
			pending = append(pending, toSync{entry: entry, isNew: true})
		} else if entry.Version > local.Version || entry.Checksum != local.Checksum {
			pending = append(pending, toSync{entry: entry, isNew: false})
		}
	}

	// 6. If no changes, print up-to-date and summary.
	if len(pending) == 0 {
		if ui.JSONMode {
			out := struct {
				Synced int      `json:"synced"`
				Failed int      `json:"failed"`
				Agents []string `json:"agents"`
				Total  int      `json:"total"`
			}{
				Synced: 0,
				Failed: 0,
				Agents: []string{},
				Total:  len(manifest),
			}
			return ui.PrintJSON(out)
		}
		if !syncQuiet {
			ui.Success("Already up to date")
			ui.Summary(
				fmt.Sprintf("0 updated"),
				fmt.Sprintf("0 failed"),
				fmt.Sprintf("%d total", len(manifest)),
			)
		}
		return nil
	}

	// 7. If --dry-run, show what would happen and return.
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
				fmt.Sprintf("%d total", len(manifest)),
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

	// Carry over skills that didn't need updating.
	for name, local := range localMap {
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

	for _, ts := range pending {
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

		// Extract to each detected agent's skills directory.
		// Track per-agent success so a partial failure doesn't corrupt state.
		extractOK := 0
		extractFail := 0
		for _, agent := range detectedAgents {
			destDir := filepath.Join(agentSkillsBase(agent, scope, home, projectRoot), ts.entry.Name)
			// Clean previous version before extracting.
			_ = os.RemoveAll(destDir)
			if err := skill.Unpack(bytes.NewReader(archive), destDir); err != nil {
				ui.Errorf("extract %s to %s: %s", ts.entry.Name, agent.Name(), err)
				extractFail++
			} else {
				extractOK++
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
				Name:     ts.entry.Name,
				Version:  ts.entry.Version,
				Checksum: ts.entry.Checksum,
			})
		}
	}

	// 10. Write new state file.
	newState := &config.SyncState{
		LastSync: time.Now().UTC().Format(time.RFC3339),
		Skills:   newSkills,
	}
	if err := config.WriteState(dir, newState); err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(fmt.Sprintf("write state: %s", err), "state_error", "")
			return nil
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
			Agents []string          `json:"agents"`
			Scope  string            `json:"scope"`
			Dests  map[string]string `json:"dests"`
			Total  int               `json:"total"`
		}{
			Synced: synced,
			Failed: failed,
			Agents: agentNames,
			Scope:  string(scope),
			Dests:  dests,
			Total:  len(manifest),
		}
		return ui.PrintJSON(out)
	}

	if !syncQuiet {
		parts := []string{
			fmt.Sprintf("%d updated", synced),
			fmt.Sprintf("%d failed", failed),
			fmt.Sprintf("%d total", len(manifest)),
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
