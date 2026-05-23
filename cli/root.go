package cli

import (
	"os"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "skael",
	Short:         "Control plane for AI agent skills",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		ui.Errorf("%s", err)
		os.Exit(1)
	}
}

func init() {
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
