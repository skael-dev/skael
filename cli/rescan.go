package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/ui"
)

var rescanCmd = &cobra.Command{
	Use:   "rescan <skill>[@version]",
	Short: "Scan a published version again on the server and print the report",
	Long: `Scan a published version again on the server, with the same steps as a
publish, and print the stored report. A version left off means the newest
version, held or released. Only an owner of the skill or an admin can rescan.

A new exfiltration or secret finding withdraws a released version: the server
stops serving it, and skael sync removes it from every machine. Any other new
finding is flagged and changes nothing.

skael scan <dir> stays the local scan before a publish.

Exit codes:
  0  the version has no blocking finding
  1  the version has a blocking finding, or the request failed
  2  usage error`,
	Args: cobra.ExactArgs(1),
	RunE: runRescan,
}

func init() { rootCmd.AddCommand(rescanCmd) }

func runRescan(cmd *cobra.Command, args []string) error {
	name, verText, hasVersion := strings.Cut(args[0], "@")
	if name == "" {
		return usageError("invalid_skill", fmt.Sprintf("%q names no skill", args[0]), "")
	}
	version := 0
	if hasVersion {
		v, err := strconv.Atoi(verText)
		if err != nil || v < 1 {
			return usageError("invalid_version", fmt.Sprintf("%q is not a version number", verText), "")
		}
		version = v
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return notConfigured(err)
	}
	c := client.New(cfg.Endpoint, cfg.APIKey)

	if version == 0 {
		versions, err := c.ListVersions(name)
		if err != nil {
			return apiFailure(err)
		}
		for _, v := range versions {
			version = max(version, v.Version)
		}
		if version == 0 {
			return usageError("no_version", name+" has no version to rescan", "publish a version first")
		}
	}

	sp := StartSpinner(fmt.Sprintf("Rescanning %s v%d...", name, version))
	res, err := c.Rescan(name, version)
	sp.Stop()
	if err != nil {
		return apiFailure(err)
	}

	if ui.JSONMode {
		if err := ui.PrintJSON(res); err != nil {
			return err
		}
	} else {
		printRescan(res)
	}
	if res.Report.Status == "critical" || res.Report.Status == "warn" {
		return renderedExit(exitAPI)
	}
	return nil
}

func printRescan(res *client.SkillScan) {
	isNew := map[string]bool{}
	for _, f := range res.NewFindings {
		isNew[rescanKey(f)] = true
	}
	fmt.Fprintf(os.Stdout, "  %s v%d · %s\n\n", res.Skill, res.Version, res.GateState)
	if res.Report.Status == "clean" {
		fmt.Fprintln(os.Stdout, "  ✓ No security findings")
	} else {
		for _, f := range res.Report.Findings {
			mark := ""
			if isNew[rescanKey(f)] {
				mark = "  new since release"
			}
			fmt.Fprintf(os.Stdout, "  %s:%d\t%-10s  %s%s\n", f.File, f.Line, f.Severity, f.Message, mark)
		}
	}
	s := res.Report.Summary
	fmt.Fprintf(os.Stdout, "\n  %d critical · %d high · %d medium · %d info\n\n", s.Critical, s.High, s.Medium, s.Info)

	switch res.Outcome {
	case "withdrawn":
		ui.Warn("%s v%d is withdrawn: %s", res.Skill, res.Version, res.GateNote)
		ui.Info("Clients remove it on their next sync. Publish a new version without the finding to release the skill again.")
	case "rejected":
		ui.Warn("%s v%d is rejected: %s", res.Skill, res.Version, res.GateNote)
	case "released":
		ui.Success("%s v%d is released: the rescan cleared its last hold reason", res.Skill, res.Version)
	case "held":
		ui.Info("%s v%d stays held for review", res.Skill, res.Version)
	default:
		if len(res.NewFindings) > 0 {
			ui.Warn("%d new %s since release; the version stays %s",
				len(res.NewFindings), plural(len(res.NewFindings), "finding", "findings"), res.GateState)
		} else {
			ui.Success("Stored the new report for %s v%d", res.Skill, res.Version)
		}
	}
}

func rescanKey(f scan.Finding) string {
	return fmt.Sprintf("%s\x00%s\x00%d", f.Rule, f.File, f.Line)
}
