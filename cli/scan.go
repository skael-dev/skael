package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan <dir>",
	Short: "Run security scan on a skill directory",
	Args:  cobra.ExactArgs(1),
	RunE:  runScan,
}

func init() { rootCmd.AddCommand(scanCmd) }

func runScan(cmd *cobra.Command, args []string) error {
	dir := args[0]

	// Check SKILL.md exists
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); os.IsNotExist(err) {
		if ui.JSONMode {
			ui.PrintJSONError("SKILL.md not found in "+dir, "missing_skill_md", "")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "SKILL.md not found in " + dir,
			Suggestion: "create a SKILL.md with name and description frontmatter",
		})
		return nil
	}

	report, err := scan.ScanDir(dir)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "scan_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	if ui.JSONMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	if report.Status == "clean" {
		fmt.Fprintln(os.Stdout, "  ✓ No security findings")
	} else {
		for _, f := range report.Findings {
			fmt.Fprintf(os.Stdout, "  %s:%d\t%-10s  %s\n",
				f.File, f.Line, f.Severity, f.Message)
		}
	}

	s := report.Summary
	fmt.Fprintf(os.Stdout, "\n  %d critical · %d high · %d medium · %d info\n",
		s.Critical, s.High, s.Medium, s.Info)

	if report.Status == "critical" {
		os.Exit(1)
	}
	return nil
}
