package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all skills on the platform",
	RunE:  runList,
}

func init() { rootCmd.AddCommand(listCmd) }

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("not configured", "not_configured", "skael setup <url> <api-key>")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "not configured",
			Suggestion: "skael setup <url> <api-key>",
		})
		return nil
	}

	c := client.New(cfg.Endpoint, cfg.APIKey)

	skills, total, err := c.ListSkills(100, 0)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "api_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	if ui.JSONMode {
		out := struct {
			Skills interface{} `json:"skills"`
			Total  int         `json:"total"`
		}{
			Skills: skills,
			Total:  total,
		}
		return ui.PrintJSON(out)
	}

	if len(skills) == 0 {
		fmt.Fprintln(os.Stdout, "  No skills published yet.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "  Try: skael publish ./my-skill")
		return nil
	}

	for _, sk := range skills {
		age := formatAge(sk.UpdatedAt)
		desc := truncate(sk.Description, 40)
		line := fmt.Sprintf("  %-24s v%d · %-8s  %s",
			sk.Name,
			sk.LatestVersion,
			age,
			desc,
		)
		fmt.Fprintln(os.Stdout, line)
	}

	fmt.Fprintf(os.Stdout, "\n  %d %s\n", total, plural(total, "skill", "skills"))
	return nil
}

// formatAge returns a human-readable age string for the given time.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	}
}

// truncate shortens s to at most max runes, appending "..." if truncated.
func truncate(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max-3]) + "..."
}

// plural returns singular when n == 1, otherwise plural.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
