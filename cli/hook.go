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
		return notConfigured(err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return failure("home_error", fmt.Sprintf("cannot determine home directory: %s", err), "")
	}

	detectedAgents := agents.DetectIn(home)
	if len(detectedAgents) == 0 {
		ui.Warn("no agents detected")
		return nil
	}

	failed := installActivationHooks(config.DefaultDir(), home, cfg.Endpoint, cfg.APIKey, detectedAgents)
	failed += installAutoSyncHooks(config.DefaultDir(), home, detectedAgents)
	if failed > 0 {
		return failure("hook_error", fmt.Sprintf("%d hook %s failed to install", failed, plural(failed, "step", "steps")), "")
	}
	return nil
}

// installActivationHooks writes the hook scripts and registers them with each
// agent. It reports each failure and returns how many occurred, so one agent
// with a broken config does not stop the others.
func installActivationHooks(dir, home, endpoint, apiKey string, detected []agents.Agent) int {
	scriptPath, err := hooks.WriteHookScript(dir)
	if err != nil {
		ui.Warn("write hook script: %s", err)
		return 1
	}
	failed := 0
	cursorScriptPath, cursorErr := hooks.WriteCursorStopScript(dir)
	if cursorErr != nil {
		ui.Warn("write cursor hook script: %s", cursorErr)
		failed++
	}
	for _, agent := range detected {
		configPath := agent.ConfigPath(home)
		if mkErr := os.MkdirAll(filepath.Dir(configPath), 0o755); mkErr != nil {
			ui.Warn("create config dir for %s: %s", agent.Name(), mkErr)
			failed++
			continue
		}
		hookScript := scriptPath
		if agent.Name() == "cursor" {
			if cursorScriptPath == "" {
				ui.Warn("skip cursor hook: script not available")
				continue
			}
			hookScript = cursorScriptPath
		}
		if instErr := hooks.InstallForAgent(agent.Name(), configPath, endpoint, apiKey, hookScript); instErr != nil {
			ui.Warn("install hook for %s: %s", agent.Name(), instErr)
			failed++
		} else {
			ui.Success("Hook installed for %s", agent.Name())
		}
	}
	return failed
}

func installAutoSyncHooks(dir, home string, detected []agents.Agent) int {
	autoSyncPath, err := hooks.WriteAutoSyncScript(dir)
	if err != nil {
		ui.Warn("write auto-sync script: %s", err)
		return 1
	}
	failed := 0
	for _, agent := range detected {
		if instErr := hooks.InstallAutoSyncForAgent(agent.Name(), agent.ConfigPath(home), autoSyncPath); instErr != nil {
			ui.Warn("install auto-sync for %s: %s", agent.Name(), instErr)
			failed++
		} else {
			ui.Success("Auto-sync installed for %s", agent.Name())
		}
	}
	return failed
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
		return failure("home_error", fmt.Sprintf("cannot determine home directory: %s", err), "")
	}

	// Check all known agents, not just detected ones.
	knownAgents := agents.Known()

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
		return failure("home_error", fmt.Sprintf("cannot determine home directory: %s", err), "")
	}

	detectedAgents := agents.DetectIn(home)
	if len(detectedAgents) == 0 {
		ui.Warn("no agents detected")
		return nil
	}

	failed := 0
	for _, agent := range detectedAgents {
		configPath := agent.ConfigPath(home)
		if err := hooks.UninstallForAgent(agent.Name(), configPath); err != nil {
			ui.Errorf("uninstall hook for %s: %s", agent.Name(), err)
			failed++
		} else {
			ui.Success("Hook uninstalled for %s", agent.Name())
		}
		// Also uninstall auto-sync hooks.
		if err := hooks.UninstallAutoSyncForAgent(agent.Name(), configPath); err != nil {
			ui.Errorf("uninstall auto-sync for %s: %s", agent.Name(), err)
			failed++
		}
	}

	if failed > 0 {
		return failure("hook_error", fmt.Sprintf("%d hook %s failed to uninstall", failed, plural(failed, "step", "steps")), "")
	}
	return nil
}
