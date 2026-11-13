package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup <url> [api-key]",
	Short: "One-command onboarding: validate, configure, sync, install hooks",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runSetup,
}

var setupSkipSync, setupSkipHooks bool
var setupNoAutoSync bool
var setupScope string

func init() {
	setupCmd.Flags().BoolVar(&setupSkipSync, "skip-sync", false, "Skip initial sync")
	setupCmd.Flags().BoolVar(&setupSkipHooks, "skip-hooks", false, "Skip hook installation")
	setupCmd.Flags().BoolVar(&setupNoAutoSync, "no-auto-sync", false, "Skip auto-sync hook installation")
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

	if apiKey == "" {
		fmt.Fprint(os.Stderr, "API key: ")
		if term.IsTerminal(int(os.Stdin.Fd())) {
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				return usageError("read_key_error", fmt.Sprintf("read API key: %s", err), "")
			}
			fmt.Fprintln(os.Stderr)
			apiKey = strings.TrimSpace(string(raw))
		} else {
			scanner := bufio.NewScanner(os.Stdin)
			if scanner.Scan() {
				apiKey = strings.TrimSpace(scanner.Text())
			}
		}
	}

	if endpoint == "" || apiKey == "" {
		return usageError("missing_credentials", "URL and API key are required", "skael setup <url> <api-key>  or set SKAEL_URL and SKAEL_KEY")
	}

	// 2. Validate connectivity.
	c := client.New(endpoint, apiKey)
	if err := c.Health(); err != nil {
		return &exitError{
			exit: exitAPI, code: "connect_error", msg: "Cannot connect to " + endpoint,
			context: err.Error(), suggestion: fmt.Sprintf("curl %s/api/health", endpoint),
		}
	}
	ui.Success("Connected to %s", endpoint)

	// 3. Write config.
	if !validScope(setupScope) {
		return usageError("invalid_scope", fmt.Sprintf("invalid --scope %q: must be \"project\" or \"user\"", setupScope), "")
	}
	dir := config.DefaultDir()
	cfg := &config.Config{
		Endpoint: endpoint,
		APIKey:   apiKey,
		Scope:    setupScope,
		Skills:   []config.SkillEntry{},
	}
	if err := config.WriteConfig(dir, cfg); err != nil {
		return failure("config_error", fmt.Sprintf("write config: %s", err), "")
	}
	ui.Success("Configuration saved")

	// 4. Detect agents.
	home, err := os.UserHomeDir()
	if err != nil {
		return failure("home_error", fmt.Sprintf("cannot determine home directory: %s", err), "")
	}
	detectedAgents := agents.DetectIn(home)

	// 5. Run initial sync unless skipped. A failure here must not abort hook
	// installation (independent step), but it must fail the command at the end.
	var syncErr error
	if !setupSkipSync {
		if syncErr = runSync(cmd, nil); syncErr != nil {
			ui.Errorf("initial sync failed: %s", syncErr)
		}
	}

	// 6. Install hooks unless skipped and agents were found. A hook failure is a
	// warning here: the skills are in place and `skael hook install` retries it.
	if !setupSkipHooks && len(detectedAgents) > 0 {
		installActivationHooks(dir, home, detectedAgents)
		if !setupNoAutoSync {
			installAutoSyncHooks(home, detectedAgents)
		}
	}

	if syncErr != nil {
		return &exitError{
			exit: exitAPI, code: "sync_failed", msg: "Setup finished with errors: initial sync failed",
			context: syncErr.Error(), suggestion: "Fix the issue, then run: skael sync",
		}
	}

	ui.Success("Setup complete. Skills are live.")
	return nil
}
