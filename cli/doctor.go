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

// doctorCheck is the machine-readable result of a single doctor check.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok" | "warn" | "fail" | "skip"
	Detail string `json:"detail,omitempty"`
}

// doctorResult pairs a machine-readable check with its pre-rendered styled line.
type doctorResult struct {
	check doctorCheck
	line  string // styled stderr line (without trailing newline)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	var results []doctorResult

	add := func(name, status, detail, styledDetail string) {
		var line string
		switch status {
		case "ok":
			line = fmt.Sprintf("  ✓ %s: %s", name, styledDetail)
		case "fail":
			line = fmt.Sprintf("  ✗ %s: %s", name, styledDetail)
		case "warn":
			line = fmt.Sprintf("  ! %s: %s", name, styledDetail)
		case "skip":
			line = fmt.Sprintf("  · %s: %s", name, styledDetail)
		}
		results = append(results, doctorResult{
			check: doctorCheck{Name: name, Status: status, Detail: detail},
			line:  line,
		})
	}

	home, _ := os.UserHomeDir()
	dir := config.DefaultDir()

	// ── 1. Config file ────────────────────────────────────────────────────────
	cfg, cfgErr := config.LoadConfig()
	if cfgErr != nil {
		detail := fmt.Sprintf("not found (%s)", cfgErr)
		add("config", "fail", detail, detail)
	} else {
		detail := cfg.Endpoint
		add("config", "ok", detail, ui.Faint(detail))
	}

	// ── 2. Platform health ────────────────────────────────────────────────────
	if cfg != nil {
		c := client.New(cfg.Endpoint, cfg.APIKey)
		_, total, err := c.ListSkills(1, 0)
		if err != nil {
			detail := fmt.Sprintf("unreachable (%s)", err)
			add("platform", "fail", detail, detail)
		} else {
			detail := fmt.Sprintf("%d skill(s)", total)
			add("platform", "ok", detail, ui.Faint(fmt.Sprintf("%d", total))+" skill(s)")
		}
	} else {
		detail := "skipped (no config)"
		add("platform", "skip", detail, detail)
	}

	// ── 3. State file ─────────────────────────────────────────────────────────
	state, stateErr := config.ReadState(dir)
	if stateErr != nil {
		detail := fmt.Sprintf("cannot read (%s)", stateErr)
		add("state", "fail", detail, detail)
	} else if len(state.Skills) == 0 {
		detail := "empty — run `skael sync`"
		add("state", "warn", detail, detail)
	} else {
		detail := fmt.Sprintf("%d skill(s) synced", len(state.Skills))
		add("state", "ok", detail, ui.Faint(fmt.Sprintf("%d", len(state.Skills)))+" skill(s) synced")
	}

	// ── 4. Per-agent checks ───────────────────────────────────────────────────
	// Resolve placement scope from config (no flag in doctor) to report the
	// directory skills actually sync to.
	var configScope string
	if cfg != nil {
		configScope = cfg.Scope
	}
	scope := resolveScope("", configScope)
	var projectRoot string
	if scope == ScopeProject {
		if wd, err := os.Getwd(); err == nil {
			projectRoot = gitRoot(wd)
		}
	}

	knownAgents := []agents.Agent{
		&agents.ClaudeCode{},
		&agents.Codex{},
		&agents.OpenCode{},
		&agents.Cursor{},
	}

	for _, agent := range knownAgents {
		name := agent.Name()

		if !agent.Detected(home) {
			add(name, "skip", "not detected", "not detected")
			continue
		}

		// Count skills in the agent's skills directory (scope-resolved).
		skillsDir := agentSkillsBase(agent, scope, home, projectRoot)
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
			if rel, err := filepath.Rel(home, skillsDir); err == nil && !strings.HasPrefix(rel, "..") {
				relSkillsDir = "~/" + rel
			}
		}

		if hookInstalled {
			detail := fmt.Sprintf("%d skill(s) in %s, hook installed", skillCount, relSkillsDir)
			styled := fmt.Sprintf("%d skill(s) in %s, hook installed", skillCount, ui.Faint(relSkillsDir))
			add(name, "ok", detail, styled)
		} else {
			detail := fmt.Sprintf("%d skill(s) in %s, hook not installed — run `skael hook install`", skillCount, relSkillsDir)
			styled := fmt.Sprintf("%d skill(s) in %s, hook not installed — run `skael hook install`", skillCount, ui.Faint(relSkillsDir))
			add(name, "warn", detail, styled)
		}
	}

	// ── Determine overall health ──────────────────────────────────────────────
	healthy := true
	for _, r := range results {
		if r.check.Status == "fail" {
			healthy = false
			break
		}
	}

	// ── JSON output ───────────────────────────────────────────────────────────
	if ui.JSONMode {
		checks := make([]doctorCheck, len(results))
		for i, r := range results {
			checks[i] = r.check
		}
		return ui.PrintJSON(struct {
			Checks  []doctorCheck `json:"checks"`
			Healthy bool          `json:"healthy"`
		}{Checks: checks, Healthy: healthy})
	}

	// ── Styled output (visually identical to original) ────────────────────────
	passed := 0
	warnings := 0
	notApplicable := 0

	for _, r := range results {
		fmt.Fprintln(os.Stderr, r.line)
		switch r.check.Status {
		case "ok":
			passed++
		case "fail", "warn":
			warnings++
		case "skip":
			notApplicable++
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
