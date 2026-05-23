package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search skills on the platform",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSearch,
}

func init() { rootCmd.AddCommand(searchCmd) }

func runSearch(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

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

	results, err := c.SearchSkills(query, 20)
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
			Results interface{} `json:"results"`
			Query   string      `json:"query"`
		}{
			Results: results,
			Query:   query,
		}
		return ui.PrintJSON(out)
	}

	if len(results) == 0 {
		fmt.Fprintf(os.Stdout, "  No results for '%s'\n", query)
		return nil
	}

	for _, sk := range results {
		desc := truncate(sk.Description, 40)
		line := fmt.Sprintf("  %-24s v%d   %s",
			sk.Name,
			sk.LatestVersion,
			desc,
		)
		fmt.Fprintln(os.Stdout, line)
	}

	n := len(results)
	fmt.Fprintf(os.Stdout, "\n  %d %s for '%s'\n", n, plural(n, "result", "results"), query)
	return nil
}
