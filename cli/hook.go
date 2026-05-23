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
		if instErr := hooks.InstallForAgent(agent.Name(), configPath, cfg.Endpoint, cfg.APIKey, scriptPath); instErr != nil {
			ui.Errorf("install hook for %s: %s", agent.Name(), instErr)
		} else {
			ui.Success("Hook installed for %s", agent.Name())
		}
	}

	return nil
}

func runHookStatus(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		ui.Errorf("cannot determine home directory: %s", err)
		return fmt.Errorf("home dir: %w", err)
	}

	// Check all known agents, not just detected ones.
	knownAgents := []agents.Agent{
		&agents.ClaudeCode{},
		&agents.Codex{},
	}

	for _, agent := range knownAgents {
		name := agent.Name()

		if !agent.Detected(home) {
			fmt.Fprintf(os.Stderr, "  · %s: not detected\n", name)
			continue
		}

		configPath := agent.ConfigPath(home)
		data, err := os.ReadFile(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s: config not readable (%s)\n", name, err)
			continue
		}

		if strings.Contains(string(data), "skael") {
			fmt.Fprintf(os.Stderr, "  ✓ %s: hook installed\n", name)
		} else {
			fmt.Fprintf(os.Stderr, "  ! %s: hook not installed\n", name)
		}
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
	}

	return nil
}
