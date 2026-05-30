package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/cli/hooks"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup <url> <api-key>",
	Short: "One-command onboarding: validate, configure, sync, install hooks",
	Args:  cobra.RangeArgs(0, 2),
	RunE:  runSetup,
}

var setupSkipSync, setupSkipHooks bool
var setupScope string

func init() {
	setupCmd.Flags().BoolVar(&setupSkipSync, "skip-sync", false, "Skip initial sync")
	setupCmd.Flags().BoolVar(&setupSkipHooks, "skip-hooks", false, "Skip hook installation")
	setupCmd.Flags().StringVar(&setupScope, "scope", "project", "Default skill placement scope: project|user")
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	// 1. Resolve URL and API key from args or environment variables.
	var endpoint, apiKey string

	if len(args) >= 1 {
		endpoint = args[0]
	}
	if len(args) >= 2 {
		apiKey = args[1]
	}

	if endpoint == "" {
		endpoint = os.Getenv("SKAEL_URL")
	}
	if apiKey == "" {
		apiKey = os.Getenv("SKAEL_KEY")
	}

	if endpoint == "" || apiKey == "" {
		ui.Error(ui.ErrorDetail{
			Message:    "URL and API key are required",
			Suggestion: "skael setup <url> <api-key>  or set SKAEL_URL and SKAEL_KEY",
		})
		return fmt.Errorf("missing credentials")
	}

	// 2. Validate connectivity.
	c := client.New(endpoint, apiKey)
	if err := c.Health(); err != nil {
		ui.Error(ui.ErrorDetail{
			Message:    "Cannot connect to " + endpoint,
			Context:    err.Error(),
			Suggestion: fmt.Sprintf("curl %s/api/health", endpoint),
		})
		return fmt.Errorf("health check failed: %w", err)
	}
	ui.Success("Connected to %s", endpoint)

	// 3. Write config.
	if !validScope(setupScope) {
		ui.Errorf("invalid --scope %q: must be \"project\" or \"user\"", setupScope)
		return fmt.Errorf("invalid scope")
	}
	dir := config.DefaultDir()
	cfg := &config.Config{
		Endpoint: endpoint,
		APIKey:   apiKey,
		Scope:    setupScope,
	}
	if err := config.WriteConfig(dir, cfg); err != nil {
		ui.Errorf("write config: %s", err)
		return fmt.Errorf("write config: %w", err)
	}
	ui.Success("Configuration saved")

	// 4. Detect agents.
	home, err := os.UserHomeDir()
	if err != nil {
		ui.Errorf("cannot determine home directory: %s", err)
		return fmt.Errorf("home dir: %w", err)
	}
	detectedAgents := agents.DetectIn(home)

	// 5. Run initial sync unless skipped.
	if !setupSkipSync {
		if err := runSync(cmd, nil); err != nil {
			ui.Warn("initial sync failed: %s", err)
		}
	}

	// 6. Install hooks unless skipped and agents were found.
	if !setupSkipHooks && len(detectedAgents) > 0 {
		scriptPath, err := hooks.WriteHookScript(dir)
		if err != nil {
			ui.Warn("write hook script: %s", err)
		} else {
			cursorScriptPath, cursorErr := hooks.WriteCursorStopScript(dir)
			if cursorErr != nil {
				ui.Warn("write cursor hook script: %s", cursorErr)
			}
			for _, agent := range detectedAgents {
				configPath := agent.ConfigPath(home)
				// Ensure parent directory exists for agents whose config may not yet exist.
				if mkErr := os.MkdirAll(filepath.Dir(configPath), 0o755); mkErr != nil {
					ui.Warn("create config dir for %s: %s", agent.Name(), mkErr)
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
				} else {
					ui.Success("Hook installed for %s", agent.Name())
				}
			}
		}
	}

	ui.Success("Setup complete. Skills are live.")
	return nil
}
