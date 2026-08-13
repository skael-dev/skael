package whetstone

import (
	"fmt"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
)

// RenderEvalSummary is the whole terminal result of an eval: the score, what
// it is made of, and every eval whose expectations did not all pass.
// Exported for its test — nothing outside this package calls it.
//
// It leads with the failures because that is what the reader can act on.
func RenderEvalSummary(rep *report.Report, evalID int64, skill string) string {
	var b strings.Builder

	mark := "✗"
	if rep.Headline > 0 {
		mark = "✓"
	}
	panel := "panel healthy"
	if !rep.PanelComplete {
		panel = "panel incomplete"
	}
	fmt.Fprintf(&b, "%s %s scored %.0f / 100\t%s tier · %s\n\n", mark, skill, rep.Headline, rep.Tier, panel)

	passed, total, failures := expectationTally(rep)
	fmt.Fprintf(&b, "  Expectations passed     %d of %d\n", passed, total)
	for _, f := range failures {
		fmt.Fprintf(&b, "\n    eval %s\n", f.taskID)
		for _, e := range f.failed {
			fmt.Fprintf(&b, "      ✗ %s\n", e)
		}
	}

	fmt.Fprintf(&b, "\n  Fires when it should    %s\n", yesNo(rep.TriggerF1))
	if rep.DeltaMeasured {
		fmt.Fprintf(&b, "  Better than no skill    %+.0f points (%.0f without it)\n", rep.Delta, rep.Baseline)
	} else {
		fmt.Fprintf(&b, "  Better than no skill    not measured — %s tier runs no baseline\n", rep.Tier)
	}

	fmt.Fprintf(&b, "\n  The score is the share of expectations passed. Full detail:  whetstone report %d\n", evalID)
	return b.String()
}

type evalFailure struct {
	taskID string
	failed []string
}

// expectationTally counts the skill condition only: the baseline exists to be
// compared against, and a baseline failure is not a defect in the skill.
//
// Failed expectations are collected from the graded runs rather than the
// per-condition totals, because the totals say how many failed and the reader
// needs to know which.
func expectationTally(rep *report.Report) (passed, total int, failures []evalFailure) {
	for _, t := range rep.Tasks {
		for _, c := range t.Conditions {
			if c.Condition != runner.CondSkill {
				continue
			}
			passed += c.Passes
			total += c.Runs
		}

		seen := map[string]bool{}
		var failed []string
		for _, g := range t.Grades {
			if g.Condition != runner.CondSkill {
				continue
			}
			for _, e := range g.Expectations {
				if e.Passed || seen[e.Text] {
					continue
				}
				seen[e.Text] = true
				failed = append(failed, e.Text)
			}
		}
		if len(failed) > 0 {
			failures = append(failures, evalFailure{taskID: t.TaskID, failed: failed})
		}
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].taskID < failures[j].taskID })
	return passed, total, failures
}

func yesNo(v float64) string {
	if v >= 0.999 {
		return "yes"
	}
	return fmt.Sprintf("%.2f", v)
}
