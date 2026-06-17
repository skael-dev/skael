package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/cli/hooks"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Manage activation tracking hooks",
}

var hookInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install skael hooks for all detected agents",
	RunE:  runHookInstall,
}

var hookStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show hook installation status for all known agents",
	RunE:  runHookStatus,
}

var hookUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall skael hooks from all detected agents",
	RunE:  runHookUninstall,
}

func init() {
	hookCmd.AddCommand(hookInstallCmd, hookStatusCmd, hookUninstallCmd)
	rootCmd.AddCommand(hookCmd)
}

func runHookInstall(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		ui.Error(ui.ErrorDetail{
			Message:    "not configured",
			Suggestion: "skael setup <url> <api-key>",
		})
		return fmt.Errorf("load config: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		ui.Errorf("cannot determine home directory: %s", err)
		return fmt.Errorf("home dir: %w", err)
	}

	dir := config.DefaultDir()
	scriptPath, err := hooks.WriteHookScript(dir)
	if err != nil {
		ui.Errorf("write hook script: %s", err)
		return fmt.Errorf("write hook script: %w", err)
	}
	cursorScriptPath, cursorErr := hooks.WriteCursorStopScript(dir)
	if cursorErr != nil {
		ui.Warn("write cursor hook script: %s", cursorErr)
	}

	detectedAgents := agents.DetectIn(home)
	if len(detectedAgents) == 0 {
		ui.Warn("no agents detected")
		return nil
	}

	for _, agent := range detectedAgents {
		configPath := agent.ConfigPath(home)
		// Ensure parent directory exists.
		if mkErr := os.MkdirAll(filepath.Dir(configPath), 0o755); mkErr != nil {
			ui.Warn("create config dir for %s: %s", agent.Name(), mkErr)
			continue
		}
		hookScript := scriptPath
		if agent.Name() == "cursor" {
			hookScript = cursorScriptPath
		}
		if instErr := hooks.InstallForAgent(agent.Name(), configPath, cfg.Endpoint, cfg.APIKey, hookScript); instErr != nil {
			ui.Errorf("install hook for %s: %s", agent.Name(), instErr)
		} else {
			ui.Success("Hook installed for %s", agent.Name())
		}
	}

	// Install auto-sync hooks.
	autoSyncPath, autoSyncErr := hooks.WriteAutoSyncScript(dir)
	if autoSyncErr != nil {
		ui.Warn("write auto-sync script: %s", autoSyncErr)
	} else {
		for _, agent := range detectedAgents {
			configPath := agent.ConfigPath(home)
			if instErr := hooks.InstallAutoSyncForAgent(agent.Name(), configPath, autoSyncPath); instErr != nil {
				ui.Errorf("install auto-sync for %s: %s", agent.Name(), instErr)
			} else {
				ui.Success("Auto-sync installed for %s", agent.Name())
			}
		}
	}

	return nil
}

// hookAgentStatus is the machine-readable status for a single agent's hook.
type hookAgentStatus struct {
	Name       string `json:"name"`
	Installed  bool   `json:"installed"`
	ConfigPath string `json:"config_path,omitempty"`
}

func runHookStatus(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(fmt.Sprintf("cannot determine home directory: %s", err), "home_dir_error", "")
			return nil
		}
		ui.Errorf("cannot determine home directory: %s", err)
		return fmt.Errorf("home dir: %w", err)
	}

	// Check all known agents, not just detected ones.
	knownAgents := []agents.Agent{
		&agents.ClaudeCode{},
		&agents.Codex{},
		&agents.OpenCode{},
		&agents.Cursor{},
	}

	type agentLine struct {
		status hookAgentStatus
		line   string // styled stderr line
	}
	var agentLines []agentLine

	for _, agent := range knownAgents {
		name := agent.Name()

		if !agent.Detected(home) {
			agentLines = append(agentLines, agentLine{
				status: hookAgentStatus{Name: name, Installed: false},
				line:   fmt.Sprintf("  · %s: not detected", name),
			})
			continue
		}

		configPath := agent.ConfigPath(home)
		data, readErr := os.ReadFile(configPath)
		if readErr != nil {
			agentLines = append(agentLines, agentLine{
				status: hookAgentStatus{Name: name, Installed: false, ConfigPath: configPath},
				line:   fmt.Sprintf("  ! %s: config not readable (%s)", name, readErr),
			})
			continue
		}

		installed := strings.Contains(string(data), "skael")
		var line string
		if installed {
			line = fmt.Sprintf("  ✓ %s: hook installed", name)
		} else {
			line = fmt.Sprintf("  ! %s: hook not installed", name)
		}
		agentLines = append(agentLines, agentLine{
			status: hookAgentStatus{Name: name, Installed: installed, ConfigPath: configPath},
			line:   line,
		})
	}

	if ui.JSONMode {
		statuses := make([]hookAgentStatus, len(agentLines))
		for i, al := range agentLines {
			statuses[i] = al.status
		}
		return ui.PrintJSON(struct {
			Agents []hookAgentStatus `json:"agents"`
		}{Agents: statuses})
	}

	for _, al := range agentLines {
		fmt.Fprintln(os.Stderr, al.line)
	}

	return nil
}

func runHookUninstall(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		ui.Errorf("cannot determine home directory: %s", err)
		return fmt.Errorf("home dir: %w", err)
	}

	detectedAgents := agents.DetectIn(home)
	if len(detectedAgents) == 0 {
		ui.Warn("no agents detected")
		return nil
	}

	for _, agent := range detectedAgents {
		configPath := agent.ConfigPath(home)
		if err := hooks.UninstallForAgent(agent.Name(), configPath); err != nil {
			ui.Errorf("uninstall hook for %s: %s", agent.Name(), err)
		} else {
			ui.Success("Hook uninstalled for %s", agent.Name())
		}
		// Also uninstall auto-sync hooks.
		if err := hooks.UninstallAutoSyncForAgent(agent.Name(), configPath); err != nil {
			ui.Errorf("uninstall auto-sync for %s: %s", agent.Name(), err)
		}
	}

	return nil
}
