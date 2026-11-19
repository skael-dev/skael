package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/ui"
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Check a skill's eval suite, and run an eval on the server",
}

var evalCheckCmd = &cobra.Command{
	Use:   "check <dir>",
	Short: "Validate the suite in <dir>/evals and name the tier it reaches",
	Long: `Load <dir>/evals/evals.json and <dir>/evals/triggers.json, validate every eval
and check, and plan each tier. The command exits 1 when a file does not load,
when an eval is void, or when the suite reaches no tier or a tier below --tier.`,
	Args: cobra.ExactArgs(1),
	RunE: runEvalCheck,
}

var evalRunCmd = &cobra.Command{
	Use:   "run <skill>[@version]",
	Short: "Queue an eval of a published skill on the server",
	Long: `Queue an eval of the latest version, or of the version after @. With --wait,
print the job events until the job ends, then print the headline. The eval runs
on a worker against the skill's stored suite.`,
	Args: cobra.ExactArgs(1),
	RunE: runEvalRun,
}

var (
	evalCheckTier string
	evalRunTier   string
	evalRunWait   bool
	evalPoll      = 5 * time.Second
)

func init() {
	evalCheckCmd.Flags().StringVar(&evalCheckTier, "tier", "", "Exit 1 unless the suite reaches this tier (smoke, full or deep)")
	evalRunCmd.Flags().StringVar(&evalRunTier, "tier", "full", "Tier to run: smoke, full or deep")
	evalRunCmd.Flags().BoolVar(&evalRunWait, "wait", false, "Print job events until the job ends, then the headline")
	evalCmd.AddCommand(evalCheckCmd, evalRunCmd)
	rootCmd.AddCommand(evalCmd)
}

type evalCheckVoid struct {
	ID     int    `json:"id"`
	Reason string `json:"reason"`
}

type evalCheckTierOut struct {
	Tier   string `json:"tier"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

func validTier(t string) bool {
	for _, x := range runner.Tiers {
		if string(x) == t {
			return true
		}
	}
	return false
}

func tierRank(t string) int {
	for i, x := range runner.Tiers {
		if string(x) == t {
			return i
		}
	}
	return -1
}

func runEvalCheck(cmd *cobra.Command, args []string) error {
	dir := args[0]
	if evalCheckTier != "" && !validTier(evalCheckTier) {
		return usageError("invalid_tier", fmt.Sprintf("unknown tier %q", evalCheckTier), "use smoke, full or deep")
	}
	set, err := suite.LoadEvalSet(dir)
	if err != nil {
		return failure("suite_invalid", err.Error(), "write "+filepath.Join(dir, "evals", "evals.json")+" in the skill-creator layout")
	}
	triggers, err := suite.LoadTriggerQueries(dir)
	if err != nil {
		return failure("suite_invalid", err.Error(), "")
	}

	var voids []evalCheckVoid
	void := map[int]bool{}
	checks := 0
	for _, r := range suite.Validate(dir, set) {
		if r.Void {
			void[r.ID] = true
			voids = append(voids, evalCheckVoid{ID: r.ID, Reason: r.Reason})
		}
	}
	for _, e := range set.Evals {
		checks += len(e.Checks)
	}
	should, shouldNot := 0, 0
	for _, q := range triggers {
		if q.ShouldTrigger {
			should++
		} else {
			shouldNot++
		}
	}
	tiers := runner.CheckTiers(set, void, triggers)
	reached := string(runner.Reached(tiers))

	if ui.JSONMode {
		out := make([]evalCheckTierOut, 0, len(tiers))
		for _, t := range tiers {
			out = append(out, evalCheckTierOut{Tier: string(t.Tier), OK: t.OK, Reason: t.Reason})
		}
		if err := ui.PrintJSON(map[string]any{
			"dir": dir, "evals": len(set.Evals), "checks": checks, "void": voids,
			"should_trigger": should, "should_not_trigger": shouldNot,
			"tiers": out, "reached": reached,
		}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "  %d evals, %d checks, %d should-trigger and %d should-not-trigger queries\n",
			len(set.Evals), checks, should, shouldNot)
		for _, v := range voids {
			fmt.Fprintf(os.Stdout, "  ✗ eval %d is void: %s\n", v.ID, v.Reason)
		}
		for _, t := range tiers {
			if t.OK {
				fmt.Fprintf(os.Stdout, "  ✓ %s\n", t.Tier)
			} else {
				fmt.Fprintf(os.Stdout, "  ✗ %s: %s\n", t.Tier, strings.TrimPrefix(t.Reason, "runner: "))
			}
		}
		if reached == "" {
			fmt.Fprintln(os.Stdout, "  tier: none")
		} else {
			fmt.Fprintf(os.Stdout, "  tier: %s\n", reached)
		}
	}

	switch {
	case len(voids) > 0, reached == "", evalCheckTier != "" && tierRank(reached) < tierRank(evalCheckTier):
		return renderedExit(exitAPI)
	}
	return nil
}

func runEvalRun(cmd *cobra.Command, args []string) error {
	name, version := args[0], 0
	if i := strings.LastIndex(name, "@"); i >= 0 {
		v, err := strconv.Atoi(name[i+1:])
		if err != nil || v < 1 {
			return usageError("invalid_version", fmt.Sprintf("invalid version in %q", args[0]), "skael eval run <skill>@<version>")
		}
		name, version = name[:i], v
	}
	if !validTier(evalRunTier) {
		return usageError("invalid_tier", fmt.Sprintf("unknown tier %q", evalRunTier), "use smoke, full or deep")
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return notConfigured(err)
	}
	c := client.New(cfg.Endpoint, cfg.APIKey)

	jobID, err := c.RunEval(name, version, evalRunTier)
	if err != nil {
		return apiFailure(err)
	}
	if !evalRunWait {
		if ui.JSONMode {
			return ui.PrintJSON(map[string]any{"skill": name, "job_id": jobID})
		}
		ui.Success("Queued job %s", jobID)
		return nil
	}
	if !ui.JSONMode {
		fmt.Fprintf(os.Stdout, "  Queued job %s\n", jobID)
	}

	var after int64
	status := ""
	for {
		p, err := c.EvalJobEvents(jobID, after)
		if err != nil {
			return apiFailure(err)
		}
		for _, e := range p.Events {
			after = e.ID
			if !ui.JSONMode {
				fmt.Fprintln(os.Stdout, "  "+describeEvent(e))
			}
		}
		if p.Job != nil {
			status = p.Job.Status
		}
		if status == "done" || status == "failed" || status == "cancelled" {
			break
		}
		time.Sleep(evalPoll)
	}

	job, err := c.GetEvalJob(jobID)
	if err != nil {
		return apiFailure(err)
	}
	if status != "done" {
		msg := fmt.Sprintf("job %s %s", jobID, status)
		if job.LastError != "" {
			msg += ": " + firstLine(job.LastError)
		}
		return failure("eval_"+status, msg, "")
	}
	q, err := c.QualityForVersion(job.SkillName, job.Version)
	if err != nil {
		return apiFailure(err)
	}
	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"skill": job.SkillName, "version": job.Version, "job_id": jobID, "quality": q})
	}
	fmt.Fprintf(os.Stdout, "\n  %s v%d headline %.0f (%s tier)\n", job.SkillName, job.Version, q.Headline, q.Tier)
	if q.Lift != nil {
		fmt.Fprintf(os.Stdout, "  lift %+.0f over a session with no skill\n", *q.Lift)
	} else {
		fmt.Fprintln(os.Stdout, "  lift not measured: no baseline ran")
	}
	if !q.PanelComplete {
		fmt.Fprintln(os.Stdout, "  The panel did not complete, so this score cannot release a held version.")
	}
	if q.SuiteDerived {
		fmt.Fprintln(os.Stdout, "  The suite is machine-derived, so this score cannot release a held version.")
	}
	fmt.Fprintf(os.Stdout, "  %s/skills/%s\n", cfg.Endpoint, job.SkillName)
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// describeEvent renders one job event as a line.
func describeEvent(e client.EvalJobEvent) string {
	ok := e.OK == nil || *e.OK
	switch e.Kind {
	case "phase":
		return "phase: " + e.Phase
	case "plan":
		return fmt.Sprintf("plan: %d sessions", e.Total)
	case "health":
		if ok {
			return "health check passed: " + e.Model
		}
		return "health check failed: " + e.Model + ": " + e.Error
	case "session_start":
		return fmt.Sprintf("session start: eval %s %s %s attempt %d", e.Task, e.Condition, e.Model, e.Attempt)
	case "session_end":
		line := fmt.Sprintf("session end: eval %s %s %s attempt %d, %s, $%.4f",
			e.Task, e.Condition, e.Model, e.Attempt, (time.Duration(e.DurationMS) * time.Millisecond).Round(time.Second), e.CostUSD)
		if !ok {
			line += ", failed: " + e.Error
		}
		return line
	case "grade":
		line := fmt.Sprintf("judge call: eval %s %s attempt %d", e.Task, e.Condition, e.Attempt)
		if !ok {
			line += ", failed: " + e.Error
		}
		return line
	case "cancelled":
		return "cancelled by " + e.Actor
	case "failed":
		return "failed: " + firstLine(e.Error)
	}
	return e.Kind
}
