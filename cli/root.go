package cli

import (
	"fmt"
	"os"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
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
	Use:           "skael",
	Short:         "Control plane for AI agent skills",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("skael %s (commit %s, built %s)\n", buildVersion, buildCommit, buildDate)
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		ui.Errorf("%s", err)
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
