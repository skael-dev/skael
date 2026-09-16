package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/contest"
	"github.com/skael-dev/skael/internal/ui"
)

var contestCmd = &cobra.Command{
	Use:   "contest <skill>[@version] <skill>[@version] [more...]",
	Short: "Run candidate versions against one suite and print the verdict",
	Long: `Run two or more candidate versions against one eval suite, in one job, on one
panel, at the same time, and print which won each task.

A version left off means the skill's released version. With no --suite the
worker derives one from every candidate, so no candidate's own claims set the
bar. A contest advises: it releases nothing and holds nothing.`,
	Args: cobra.MinimumNArgs(2),
	RunE: runContest,
}

var (
	contestSuite    string
	contestTier     string
	contestAttempts int
	contestWait     bool
)

func init() {
	contestCmd.Flags().StringVar(&contestSuite, "suite", "",
		"Ref of a stored eval suite; omitted, one is derived from every candidate")
	contestCmd.Flags().StringVar(&contestTier, "tier", "", "Eval tier (default full)")
	contestCmd.Flags().IntVar(&contestAttempts, "attempts", 0,
		fmt.Sprintf("Attempts per task per candidate (default %d)", contest.DefaultAttempts))
	contestCmd.Flags().BoolVar(&contestWait, "wait", true, "Wait for the verdict")
	rootCmd.AddCommand(contestCmd)
}

func runContest(cmd *cobra.Command, args []string) error {
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

	candidates := make([]client.ContestCandidate, 0, len(args))
	for _, arg := range args {
		name, version, err := contest.ParseCandidate(arg)
		if err != nil {
			ui.Errorf("%s", err)
			os.Exit(1)
		}
		candidates = append(candidates, client.ContestCandidate{Skill: name, Version: version})
	}

	result, err := c.CreateContest(candidates, contestSuite, contestTier, contestAttempts)
	if err != nil {
		ui.Errorf("%s", err)
		os.Exit(1)
	}

	if !contestWait {
		if ui.JSONMode {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		ui.Info("contest %s queued", result.ID)
		return nil
	}

	final, err := waitForContest(c, result.ID)
	if err != nil {
		ui.Errorf("%s", err)
		os.Exit(1)
	}
	if ui.JSONMode {
		return json.NewEncoder(os.Stdout).Encode(final)
	}
	printContest(final)
	return nil
}

// waitForContest polls until the verdict lands. A contest runs every candidate
// in one job, so there is one thing to wait for.
func waitForContest(c *client.Client, id string) (*client.Contest, error) {
	spin := StartSpinner("running the candidates")
	defer spin.Stop()

	for {
		cur, err := c.GetContest(id)
		if err != nil {
			return nil, err
		}
		switch cur.Status {
		case "done":
			return cur, nil
		case "failed":
			return nil, fmt.Errorf("contest %s failed: %s", id, cur.LastError)
		}
		time.Sleep(5 * time.Second)
	}
}

func printContest(c *client.Contest) {
	var labels []string
	for _, cand := range c.Candidates {
		labels = append(labels, cand.Label)
	}
	fmt.Printf("\n%s\n", ui.Bold(strings.Join(labels, "  vs  ")))
	fmt.Printf("%s · %d attempts per task · %s\n\n", c.SuiteNote, c.Attempts, c.Tier)

	for _, p := range c.Pairings {
		fmt.Printf("%s\n", ui.Bold(p.A+" vs "+p.B))
		for _, t := range p.Tasks {
			mark := "  tie "
			switch t.Winner {
			case p.A:
				mark = "  " + ui.Accent("A") + "    "
			case p.B:
				mark = "  " + ui.Accent("B") + "    "
			}
			fmt.Printf("%s %-24s %.0f%% / %.0f%%\n", mark, t.TaskID, t.ARate*100, t.BRate*100)
		}
		fmt.Printf("\n  %d-%d with %d ties, p = %.3f\n\n", p.AWins, p.BWins, p.Ties, p.PValue)
	}

	if c.Outcome == string(contest.OutcomeWinner) {
		ui.Success("%s", c.Detail)
	} else {
		ui.Info("%s", c.Detail)
	}

	if len(c.Losses) > 0 {
		fmt.Printf("\n%s\n", ui.Bold("What each candidate missed"))
		for _, l := range c.Losses {
			fmt.Printf("  %s on %s\n", l.Label, l.TaskID)
			for _, m := range l.Missed {
				fmt.Printf("    - %s\n", m)
			}
		}
	}
}
