package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check skael configuration and health",
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	passed := 0
	warnings := 0
	notApplicable := 0

	home, _ := os.UserHomeDir()
	dir := config.DefaultDir()

	// ── 1. Config file ────────────────────────────────────────────────────────
	cfg, cfgErr := config.LoadConfig()
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "  ✗ config: not found (%s)\n", cfgErr)
		warnings++
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ config: %s\n", ui.Faint(cfg.Endpoint))
		passed++
	}

	// ── 2. Platform health ────────────────────────────────────────────────────
	if cfg != nil {
		c := client.New(cfg.Endpoint, cfg.APIKey)
		_, total, err := c.ListSkills(1, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ platform: unreachable (%s)\n", err)
			warnings++
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ platform: %s skill(s)\n", ui.Faint(fmt.Sprintf("%d", total)))
			passed++
		}
	} else {
		fmt.Fprintf(os.Stderr, "  · platform: skipped (no config)\n")
		notApplicable++
	}

	// ── 3. State file ─────────────────────────────────────────────────────────
	state, stateErr := config.ReadState(dir)
	if stateErr != nil {
		fmt.Fprintf(os.Stderr, "  ✗ state: cannot read (%s)\n", stateErr)
		warnings++
	} else if len(state.Skills) == 0 {
		fmt.Fprintf(os.Stderr, "  ! state: empty — run `skael sync`\n")
		warnings++
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ state: %s skill(s) synced\n", ui.Faint(fmt.Sprintf("%d", len(state.Skills))))
		passed++
	}

	// ── 4. Per-agent checks ───────────────────────────────────────────────────
	knownAgents := []agents.Agent{
		&agents.ClaudeCode{},
		&agents.Codex{},
	}

	for _, agent := range knownAgents {
		name := agent.Name()

		if !agent.Detected(home) {
			fmt.Fprintf(os.Stderr, "  · %s: not detected\n", name)
			notApplicable++
			continue
		}

		// Count skills in the agent's skills directory.
		skillsDir := agent.SkillsDir(home)
		entries, err := os.ReadDir(skillsDir)
		skillCount := 0
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					skillCount++
				}
			}
		}

		// Check whether hook is installed by reading the agent config file.
		configPath := agent.ConfigPath(home)
		hookInstalled := false
		if data, err := os.ReadFile(configPath); err == nil {
			hookInstalled = strings.Contains(string(data), "skael")
		}

		// Determine the skills directory relative to home for display.
		relSkillsDir := skillsDir
		if home != "" {
			if rel, err := filepath.Rel(home, skillsDir); err == nil {
				relSkillsDir = "~/" + rel
			}
		}

		if hookInstalled {
			fmt.Fprintf(os.Stderr, "  ✓ %s: %d skill(s) in %s, hook installed\n",
				name, skillCount, ui.Faint(relSkillsDir))
			passed++
		} else {
			fmt.Fprintf(os.Stderr, "  ! %s: %d skill(s) in %s, hook not installed — run `skael hook install`\n",
				name, skillCount, ui.Faint(relSkillsDir))
			warnings++
		}
	}

	// ── 5. Summary ────────────────────────────────────────────────────────────
	fmt.Fprintf(os.Stderr, "\n")
	ui.Summary(
		fmt.Sprintf("%d passed", passed),
		fmt.Sprintf("%d warnings", warnings),
		fmt.Sprintf("%d not applicable", notApplicable),
	)

	return nil
}
