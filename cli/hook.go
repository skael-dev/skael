package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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

// The agent-facing commands exit 0 and print nothing on every path: a hook
// must never block or break a session, and Claude Code shows a
// UserPromptSubmit hook's stdout to the model.
var hookReportCmd = &cobra.Command{
	Use:    "report",
	Short:  "Report a skill load from an agent hook payload on stdin",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		agent, _ := cmd.Flags().GetString("agent")
		ev := hooks.Report(agent, os.Stdin, config.DefaultDir())
		if ev == nil {
			return
		}
		raw, err := json.Marshal(ev)
		if err != nil {
			return
		}
		if bin, err := os.Executable(); err == nil {
			_ = hooks.Detach(bin, "hook", "send", string(raw))
		}
	},
}

var hookSendCmd = &cobra.Command{
	Use:    "send <event-json>",
	Short:  "Post one activation event",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var ev hooks.Event
		if json.Unmarshal([]byte(args[0]), &ev) == nil {
			_ = hooks.Send(ev, config.DefaultDir())
		}
	},
}

var hookAutoSyncCmd = &cobra.Command{
	Use:    "autosync",
	Short:  "Start a background sync when the last one is 30 minutes old",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		drainStdin()
		if !hooks.AutoSyncDue(config.DefaultDir(), time.Now()) {
			return
		}
		if bin, err := os.Executable(); err == nil {
			_ = hooks.Detach(bin, "sync", "--quiet")
		}
	},
}

// drainStdin reads the agent's payload so the agent's write does not fail,
// but never waits on a stdin that stays open.
func drainStdin() {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func init() {
	hookReportCmd.Flags().String("agent", "", "Agent that ran the hook")
	hookCmd.AddCommand(hookInstallCmd, hookStatusCmd, hookUninstallCmd, hookReportCmd, hookSendCmd, hookAutoSyncCmd)
	rootCmd.AddCommand(hookCmd)
}

func runHookInstall(cmd *cobra.Command, args []string) error {
	if _, err := config.LoadConfig(); err != nil {
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

	failed := installActivationHooks(config.DefaultDir(), home, detectedAgents)
	failed += installAutoSyncHooks(home, detectedAgents)
	if failed > 0 {
		return failure("hook_error", fmt.Sprintf("%d hook %s failed to install", failed, plural(failed, "step", "steps")), "")
	}
	return nil
}

// installActivationHooks registers `skael hook report` with each agent. It
// reports each failure and returns how many occurred, so one agent with a
// broken config does not stop the others.
func installActivationHooks(dir, home string, detected []agents.Agent) int {
	bin, _, err := hooks.Binary()
	if err != nil {
		ui.Warn("find the skael binary: %s", err)
		return 1
	}
	hooks.RemoveLegacyScripts(dir)
	failed := 0
	for _, agent := range detected {
		if err := hooks.InstallForAgent(agent.Name(), agent.ConfigPath(home), bin); err != nil {
			ui.Warn("install hook for %s: %s", agent.Name(), err)
			failed++
		} else {
			ui.Success("Hook installed for %s", agent.Name())
		}
	}
	return failed
}

func installAutoSyncHooks(home string, detected []agents.Agent) int {
	bin, _, err := hooks.Binary()
	if err != nil {
		ui.Warn("find the skael binary: %s", err)
		return 1
	}
	failed := 0
	for _, agent := range detected {
		if err := hooks.InstallAutoSyncForAgent(agent.Name(), agent.ConfigPath(home), bin); err != nil {
			ui.Warn("install auto-sync for %s: %s", agent.Name(), err)
			failed++
		} else {
			ui.Success("Auto-sync installed for %s", agent.Name())
		}
	}
	return failed
}

// repairHooks rewrites the skael hooks that name an old script or another
// binary, so an upgrade or a move needs no `skael hook install`. Only the
// skael on PATH repairs, so a dev build or a copy in a temp dir never
// redirects a user's hooks to itself.
func repairHooks(args []string) {
	if len(args) > 0 && (args[0] == "hook" || args[0] == "version" || args[0] == "help") {
		return
	}
	dir := config.DefaultDir()
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		return
	}
	bin, onPath, err := hooks.Binary()
	if err != nil || !onPath {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, agent := range agents.DetectIn(home) {
		_ = hooks.Repair(agent.Name(), agent.ConfigPath(home), bin)
	}
	hooks.RemoveLegacyScripts(dir)
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
	bin, _, _ := hooks.Binary()

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
		state := hooks.State(name, configPath, bin)
		installed := state.ActivationCurrent
		var line string
		switch {
		case installed:
			line = fmt.Sprintf("  ✓ %s: hook installed", name)
		case state.Activation:
			line = fmt.Sprintf("  ! %s: hook out of date — run `skael hook install`", name)
		default:
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
