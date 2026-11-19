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
on a worker against the skill's stored suite, at the deepest tier the suite
fills unless --tier names one. A tier the suite cannot fill is refused.

With --set, the worker also installs every skill of the set beside the skill,
and the report names the share of prompts each one took and the rules the
skill keeps alone but loses with the set. The server pins the set's current
resolution; --resolution pins an earlier one, so a re-run installs the same
versions. The set never changes the headline, the lift or the gate.`,
	Args: cobra.ExactArgs(1),
	RunE: runEvalRun,
}

var (
	evalCheckTier string
	evalRunTier   string
	evalRunWait   bool
	evalRunSet    string
	evalRunRes    string
	evalPoll      = 5 * time.Second
)

func init() {
	evalCheckCmd.Flags().StringVar(&evalCheckTier, "tier", "", "Exit 1 unless the suite reaches this tier (smoke, full or deep)")
	evalRunCmd.Flags().StringVar(&evalRunTier, "tier", "", "Tier to run: smoke, full or deep (default: the deepest the suite fills)")
	evalRunCmd.Flags().BoolVar(&evalRunWait, "wait", false, "Print job events until the job ends, then the headline")
	evalRunCmd.Flags().StringVar(&evalRunSet, "set", "", "Install every skill of this set beside the skill")
	evalRunCmd.Flags().StringVar(&evalRunRes, "resolution", "", "Pin this stored resolution of --set instead of the current one")
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
	if evalRunTier != "" && !validTier(evalRunTier) {
		return usageError("invalid_tier", fmt.Sprintf("unknown tier %q", evalRunTier), "use smoke, full or deep")
	}
	if evalRunRes != "" && evalRunSet == "" {
		return usageError("resolution_without_set", "--resolution needs --set", "skael eval run <skill> --set <name> --resolution <id>")
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return notConfigured(err)
	}
	c := client.New(cfg.Endpoint, cfg.APIKey)

	queued, err := c.RunEval(name, client.RunEvalRequest{
		Version: version, Tier: evalRunTier, Set: evalRunSet, SetResolution: evalRunRes,
	})
	if err != nil {
		return apiFailure(err)
	}
	jobID, tier := queued.JobID, queued.Tier
	if !evalRunWait {
		if ui.JSONMode {
			return ui.PrintJSON(map[string]any{"skill": name, "job_id": jobID, "tier": tier, "set": queued.Set})
		}
		ui.Success("Queued job %s (%s tier)", jobID, tier)
		printPinnedSet(queued.Set)
		return nil
	}
	if !ui.JSONMode {
		fmt.Fprintf(os.Stdout, "  Queued job %s (%s tier)\n", jobID, tier)
		printPinnedSet(queued.Set)
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
	if q.Report != nil && q.Report.Set != nil {
		printSetResult(job.SkillName, q.Report.Set)
	} else if queued.Set != nil {
		fmt.Fprintln(os.Stdout, "  The with-set results are in the report, which only an owner or an admin can read while the version is held.")
	}
	fmt.Fprintf(os.Stdout, "  %s/skills/%s\n", cfg.Endpoint, job.SkillName)
	return nil
}

// printPinnedSet names the resolution a job pinned and the skills it installs.
func printPinnedSet(s *client.EvalJobSet) {
	if s == nil || ui.JSONMode {
		return
	}
	var names []string
	for _, m := range s.Members {
		names = append(names, fmt.Sprintf("%s@%d", m.Name, m.Version))
	}
	fmt.Fprintf(os.Stdout, "  Set %s, resolution %s: %s\n", s.Name, s.ResolutionID, strings.Join(names, ", "))
}

// printSetResult prints what the set's members did beside the skill.
func printSetResult(skill string, s *client.EvalSetResult) {
	fmt.Fprintf(os.Stdout, "\n  With set %s\n", s.Name)
	took := false
	for _, sh := range s.Shares {
		if sh.Took == 0 && sh.Shared == 0 {
			continue
		}
		took = true
		fmt.Fprintf(os.Stdout, "  loses %.0f%% of its prompts to %s (%d of %d)", sh.Share*100, sh.Name, sh.Took, sh.Of)
		if sh.Shared > 0 {
			fmt.Fprintf(os.Stdout, ", and fires beside it on %d more", sh.Shared)
		}
		fmt.Fprintln(os.Stdout)
	}
	measured := false
	for _, sh := range s.Shares {
		measured = measured || sh.Of > 0
	}
	switch {
	case !measured:
		fmt.Fprintln(os.Stdout, "  share of prompts not measured: this tier runs no trigger probe")
	case !took:
		fmt.Fprintf(os.Stdout, "  no member of the set took a prompt from %s\n", skill)
	}
	for _, r := range s.RulesLost {
		// A check's text already starts with "check:".
		text := r.Text
		if !r.Check {
			text = "expectation: " + text
		}
		fmt.Fprintf(os.Stdout, "  rule lost on eval %s, %s (passes %d of %d alone, %d of %d with the set)\n",
			r.TaskID, text, r.AlonePasses, r.AloneRuns, r.SetPasses, r.SetRuns)
	}
	if len(s.RulesLost) == 0 {
		fmt.Fprintln(os.Stdout, "  no expectation or check passes alone and fails with the set")
	}
	fmt.Fprintf(os.Stdout, "  Re-run with the same versions: skael eval run %s --set %s --resolution %s\n", skill, s.Name, s.ResolutionID)
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
