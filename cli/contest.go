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
	Use:   "contest [<skill>[@version]...] [--set <name>...]",
	Short: "Run candidate versions or sets against one suite and print the verdict",
	Long: `Run two or more candidates against one eval suite, in one job, on one panel,
at the same time, and print which won each task.

A candidate is a skill, with an optional version, or a set given with --set.
A set candidate installs every skill of the set's current resolution. A
version left off means the skill's released version. With no --suite the
worker derives one from every candidate, and from every member of each set.
A contest advises: it releases nothing and holds nothing.`,
	Example: `  skael contest payments:deploy@3 platform:deploy@7
  skael contest review:all@3 --set review-split`,
	RunE: runContest,
}

var (
	contestSuite    string
	contestTier     string
	contestAttempts int
	contestWait     bool
	contestSets     []string
)

func init() {
	contestCmd.Flags().StringVar(&contestSuite, "suite", "",
		"Ref of a stored eval suite; omitted, one is derived from every candidate")
	contestCmd.Flags().StringVar(&contestTier, "tier", "", "Eval tier (default full)")
	contestCmd.Flags().IntVar(&contestAttempts, "attempts", 0,
		fmt.Sprintf("Attempts per task per candidate (default %d)", contest.DefaultAttempts))
	contestCmd.Flags().BoolVar(&contestWait, "wait", true, "Wait for the verdict")
	contestCmd.Flags().StringArrayVar(&contestSets, "set", nil, "A set to enter as one candidate; repeat for more")
	rootCmd.AddCommand(contestCmd)
}

func runContest(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return notConfigured(err)
	}
	c := client.New(cfg.Endpoint, cfg.APIKey)

	if len(args)+len(contestSets) < 2 {
		return usageError("too_few_candidates", "a contest needs at least two candidates",
			"name skills as arguments and sets with --set, for example: skael contest review:all@3 --set review-split")
	}
	candidates := make([]client.ContestEntry, 0, len(args)+len(contestSets))
	for _, arg := range args {
		name, version, err := contest.ParseCandidate(arg)
		if err != nil {
			return usageError("invalid_candidate", err.Error(), "")
		}
		candidates = append(candidates, client.ContestEntry{Skill: name, Version: version})
	}
	for _, set := range contestSets {
		if strings.TrimSpace(set) == "" {
			return usageError("invalid_candidate", "--set needs a set name", "")
		}
		candidates = append(candidates, client.ContestEntry{Set: strings.TrimSpace(set)})
	}

	result, err := c.CreateContest(candidates, contestSuite, contestTier, contestAttempts)
	if err != nil {
		return apiFailure(err)
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
		return apiFailure(err)
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

	for _, cand := range c.Candidates {
		line := fmt.Sprintf("  %-32s %s", cand.Label, formatTokens(cand.TokensMedian))
		if cand.Set != nil {
			var names []string
			for _, m := range cand.Set.Members {
				names = append(names, fmt.Sprintf("%s@%d", m.Name, m.Version))
			}
			line += "\n    installs " + strings.Join(names, ", ")
		}
		fmt.Println(line)
	}
	fmt.Println()

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
			fmt.Printf("%s %-24s %.0f%% / %.0f%%%s\n", mark, t.TaskID, t.ARate*100, t.BRate*100, firedNote(c, p, t.TaskID))
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
			if len(l.Fired) > 0 {
				fmt.Printf("    fired: %s\n", strings.Join(l.Fired, ", "))
			} else if isSetLabel(c, l.Label) {
				fmt.Println("    fired: no skill of the set")
			}
			for _, m := range l.Missed {
				fmt.Printf("    - %s\n", m)
			}
		}
	}
}

// firedNote names the skills that fired on a task for each side of a pairing,
// when the contest enters a set. Without a set, the only skill that can fire
// is the candidate itself, and the column is noise.
func firedNote(c *client.Contest, p client.ContestPairing, taskID string) string {
	hasSet := false
	for _, cand := range c.Candidates {
		if cand.Set != nil {
			hasSet = true
		}
	}
	if !hasSet {
		return ""
	}
	side := func(label string) string {
		for _, cand := range c.Candidates {
			if cand.Label != label {
				continue
			}
			for _, f := range cand.Fired {
				if f.TaskID == taskID {
					return strings.Join(f.Skills, "+")
				}
			}
		}
		return "none"
	}
	return fmt.Sprintf("   fired %s / %s", side(p.A), side(p.B))
}

// formatTokens renders a candidate's median token spend per run. Zero means
// no run recorded its tokens, which is not a cheap candidate.
func formatTokens(n int64) string {
	if n == 0 {
		return "token use not measured"
	}
	return fmt.Sprintf("median %d tokens per run", n)
}

// isSetLabel reports whether label names a set candidate.
func isSetLabel(c *client.Contest, label string) bool {
	for _, cand := range c.Candidates {
		if cand.Label == label {
			return cand.Set != nil
		}
	}
	return false
}
