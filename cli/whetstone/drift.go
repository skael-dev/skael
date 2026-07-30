package whetstone

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/ui"
)

var driftCmd = &cobra.Command{
	Use:   "drift <skill> [ref]",
	Short: "Show the per-member adherence breakdown for one eval",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		skill := args[0]
		ref := "latest"
		if len(args) > 1 {
			ref = args[1]
		}

		// --json prints the stored document unchanged: report.Load then
		// re-marshaling it back out would be a second, potentially different,
		// encoding of the same fact the store already holds.
		if ui.JSONMode {
			return printStoredReport(skill, ref)
		}

		rep, err := RunDrift(cmd.Context(), skill, ref)
		if err != nil {
			return err
		}
		renderDrift(rep)
		return nil
	},
}

// RunDrift loads the report for skill's ref ("latest" or an eval id) and
// returns it, so a caller (the command above, or a test) gets the same typed
// document `whetstone eval` produced.
func RunDrift(_ context.Context, skill, ref string) (*report.Report, error) {
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()

	doc, evalID, err := resolveReportDoc(st, skill, ref)
	if err != nil {
		return nil, err
	}
	rep, err := report.Load(bytes.NewReader(doc))
	if err != nil {
		return nil, fmt.Errorf("whetstone drift: eval %d: %w", evalID, err)
	}
	return rep, nil
}

// printStoredReport writes the stored report document for skill's ref
// straight to stdout, byte for byte.
func printStoredReport(skill, ref string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	doc, _, err := resolveReportDoc(st, skill, ref)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(doc)
	return err
}

// resolveReportDoc resolves ref ("latest" or an eval id) against skill's
// stored reports, returning the raw document and the eval id it belongs to.
// An unknown skill (or an id with no report) says what to run rather than
// just "not found" — that leaves a reader guessing whether they mistyped the
// skill or never ran an eval.
func resolveReportDoc(st *store.Store, skill, ref string) ([]byte, int64, error) {
	if ref == "" || ref == "latest" {
		doc, evalID, err := st.LatestReport(skill)
		if err != nil {
			return nil, 0, fmt.Errorf("whetstone drift: no eval found for %q; run `whetstone eval %s` first", skill, skill)
		}
		return doc, evalID, nil
	}

	evalID, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("whetstone drift: %q is not a valid eval id or \"latest\"", ref)
	}
	doc, err := st.Report(evalID)
	if err != nil {
		return nil, 0, fmt.Errorf("whetstone drift: no report for eval %d of %q; run `whetstone eval %s` first", evalID, skill, skill)
	}
	return doc, evalID, nil
}

// renderDrift prints one table per panel member: the six drift components
// that make up its adherence score, followed by every violation and its
// evidence.
func renderDrift(rep *report.Report) {
	ui.Info("drift for %s (eval suite %s, tier %s)", rep.Skill, rep.SuiteRef, rep.Tier)
	for _, m := range rep.Members {
		if !m.Healthy {
			ui.Warn("%s/%s: unhealthy (%s)", m.Member.Agent, m.Member.Model, m.Detail)
			continue
		}
		ui.Info("%s/%s: adherence %.1f (grade %s)", m.Member.Agent, m.Member.Model, m.Drift.Mean, m.DriftGrade)
		for _, t := range rep.Tasks {
			for _, d := range t.Drift {
				if d.Model != m.Member.Model {
					continue
				}
				ui.Info("  %s attempt %d: adherence %.1f (coverage=%.2f order=%.2f violation=%.2f checkpoint=%.2f semantic=%.2f focus=%.2f)",
					t.TaskID, d.Attempt, d.Adherence,
					d.Components.StepCoverage, d.Components.Order, d.Components.Violation,
					d.Components.Checkpoint, d.Components.Semantic, d.Components.Focus)
				for _, v := range d.Violations {
					ui.Warn("    %s (%s, hits=%d): %v", v.ID, v.Severity, v.Hits, v.Evidence)
				}
			}
		}
	}
}

func init() {
	rootCmd.AddCommand(driftCmd)
}
