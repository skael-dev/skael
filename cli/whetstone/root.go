// Package whetstone implements the whetstone CLI: the authoring front end for
// the skill specification, generation, lint, contract, and suite engine under
// internal/eval. Commands that need a sandbox (eval, drift, repair, report)
// are not here yet.
package whetstone

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/ui"

	// The agent adapters register themselves from init(), so they are only
	// reachable through agent.Get once their package is linked in. Without
	// these blank imports agent.Get returns (nil, false) for that name — no
	// compile error and no panic, just an adapter silently missing from every
	// model panel. They live here rather than in cmd/whetstone so that the
	// package's own tests link them too.
	_ "github.com/skael-dev/skael/internal/eval/agent/claudecode"
	_ "github.com/skael-dev/skael/internal/eval/agent/codex"
	_ "github.com/skael-dev/skael/internal/eval/agent/cursor"
	_ "github.com/skael-dev/skael/internal/eval/agent/opencode"
)

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

// SetVersion is called by main to inject build-time version info.
func SetVersion(version, commit, date string) {
	buildVersion = version
	buildCommit = commit
	buildDate = date
}

var rootCmd = &cobra.Command{
	Use:           "whetstone",
	Short:         "Author, lint, and evaluate agent skills",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("whetstone %s (commit %s, built %s)\n", buildVersion, buildCommit, buildDate)
	},
}

// Execute runs the root command. A command that runs sandboxed sessions
// (eval, suite check) passes cmd.Context() into docker.Driver.Run and
// suite.Check, both of which already clean up their containers and networks
// on context cancellation — but only if something actually cancels that
// context on SIGINT/SIGTERM. Without signal.NotifyContext here, Ctrl-C is
// process death: the docker client is killed with no chance to run its own
// cleanup, and every in-flight run leaks a whetstone-net-* network and a
// whetstone-proxy-* container.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		ui.Errorf("%s", err)
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "", "")
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().BoolVar(&ui.JSONMode, "json", false, "Output structured JSON")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable color output")

	// Apply no-color flag before any command runs
	cobra.OnInitialize(func() {
		noColor, _ := rootCmd.PersistentFlags().GetBool("no-color")
		if noColor {
			os.Setenv("NO_COLOR", "1")
		}
	})
}
