package cli

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

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

	// A repo with no skills yet has no <repo>/.claude/skills; create it.
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("extractSkillAtomically: create parent dir: %w", err)
	}

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
		return notConfigured(err)
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

	if syncScope != "" && !validScope(syncScope) {
		return usageError("invalid_scope", fmt.Sprintf("invalid --scope %q: must be \"project\" or \"user\"", syncScope), "")
	}
	sp := StartSpinner("Syncing...")
	res, err := reconcile(client.New(cfg.Endpoint, cfg.APIKey), cfg, dir,
		reconcileOptions{DryRun: syncDryRun, Quiet: syncQuiet, Agent: syncAgent, Scope: syncScope})
	sp.Stop()
	if err != nil {
		return apiFailure(err)
	}
	return reportSync(res, syncQuiet, syncDryRun)
}

// reportSync prints what a reconcile did. A conflict or a failed skill makes
// the command fail after the rest has synced.
func reportSync(res *reconcileResult, quiet, dryRun bool) error {
	dests := make(map[string]string, len(res.Targets))
	labels := make([]string, 0, len(res.Targets))
	for _, t := range res.Targets {
		dests[t.Label], labels = t.Base, append(labels, t.Label)
	}
	for _, c := range res.Conflicts {
		ui.Errorf("version conflict: %s", c)
	}
	failed := res.Failed + len(res.Conflicts)
	if ui.JSONMode {
		out := struct {
			Synced    int               `json:"synced"`
			Failed    int               `json:"failed"`
			Pruned    int               `json:"pruned"`
			Agents    []string          `json:"agents"`
			Scope     string            `json:"scope"`
			Dests     map[string]string `json:"dests"`
			Total     int               `json:"total"`
			Conflicts []string          `json:"conflicts"`
		}{res.Synced, res.Failed, res.Pruned, labels, string(res.Scope), dests, res.Total, append([]string{}, res.Conflicts...)}
		if failed > 0 {
			return syncFailed(failed, map[string]any{"result": out})
		}
		return ui.PrintJSON(out)
	}
	if !quiet {
		if dryRun {
			ui.Info("dry run: nothing was written")
		}
		if res.Synced == 0 && res.Pruned == 0 && failed == 0 && !dryRun {
			ui.Success("Already up to date")
		}
		ui.Summary(
			fmt.Sprintf("%d updated", res.Synced),
			fmt.Sprintf("%d failed", failed),
			fmt.Sprintf("%d removed", res.Pruned),
			fmt.Sprintf("%d total", res.Total),
		)
		for _, t := range res.Targets {
			ui.Info("  %s · %s", res.Scope, t.Base)
		}
	}
	if failed > 0 {
		return syncFailed(failed, nil)
	}
	return nil
}

func syncFailed(failed int, extra map[string]any) *exitError {
	return &exitError{
		exit: exitAPI, code: "sync_failed", extra: extra,
		msg: fmt.Sprintf("%d %s failed to sync", failed, plural(failed, "skill", "skills")),
	}
}
