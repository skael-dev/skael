package whetstone

import (
	"fmt"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/score"
)

// RenderEvalSummary is the whole terminal result of an eval: the score, every
// task that failed with the verifier's own reason, and the pillars in words.
// Exported for its test — nothing outside this package calls it.
//
// It leads with the failures because that is what the reader can act on. A
// score with no reason attached is the defect this replaces: the reasons were
// always computed, and were thrown away before anything could print them.
func RenderEvalSummary(rep *report.Report, evalID int64, skill string, baselinePlanned bool) string {
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

	passed, total, failures := taskTally(rep)
	fmt.Fprintf(&b, "  Tasks passed        %d of %d\n", passed, total)
	for _, f := range failures {
		fmt.Fprintf(&b, "\n    %s\n", f.taskID)
		if f.reason != "" {
			fmt.Fprintf(&b, "      %s\n", f.reason)
		}
	}

	if p, ok := weakestPillars(rep); ok {
		fmt.Fprintf(&b, "\n  Fires when it should    %s\n", yesNo(p.TriggerF1))
		fmt.Fprintf(&b, "  Stays on task           %s\n", yesNo(p.Efficiency))
		if baselinePlanned {
			fmt.Fprintf(&b, "  Better than no skill    %.2f\n", p.Uplift)
		} else {
			fmt.Fprintf(&b, "  Better than no skill    not measured — %s tier runs no baseline\n", rep.Tier)
		}
	}

	fmt.Fprintf(&b, "\n  The score is the weakest of the above. Full detail:  whetstone report %d\n", evalID)
	return b.String()
}

type taskFailure struct{ taskID, reason string }

// taskTally counts the skill condition only: the baseline exists to be
// compared against, and a baseline failure is not a defect in the skill. A
// task with a multi-member panel carries one skill ConditionReport per
// member (see the per-member loop in RunEvalWith), so it is tallied once per
// TaskID, not once per condition: it passes only if every member's skill run
// passed, consistent with the headline being the panel's weakest-member
// score. A task both members failed keeps a single failure line — one of the
// reasons, not one line each, matching firstFailureReason's own budget.
func taskTally(rep *report.Report) (passed, total int, failures []taskFailure) {
	for _, t := range rep.Tasks {
		var present bool
		allPassed := true
		var reason string
		for _, c := range t.Conditions {
			if c.Condition != runner.CondSkill {
				continue
			}
			present = true
			if c.Passes <= 0 {
				allPassed = false
				if reason == "" {
					reason = c.Reason
				}
			}
		}
		if !present {
			continue
		}
		total++
		if allPassed {
			passed++
		} else {
			failures = append(failures, taskFailure{taskID: t.TaskID, reason: reason})
		}
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].taskID < failures[j].taskID })
	return passed, total, failures
}

// weakestPillars returns the healthy member whose Effectiveness is lowest —
// the member the headline is taken from, so the one whose numbers explain it.
// The low-water mark is tracked explicitly rather than compared against a
// field of `out`, which would compare the wrong quantity.
func weakestPillars(rep *report.Report) (score.Pillars, bool) {
	var out score.Pillars
	lowest := 0.0
	found := false
	for _, m := range rep.Members {
		if !m.Healthy {
			continue
		}
		if !found || m.Effectiveness < lowest {
			out, lowest, found = m.Pillars, m.Effectiveness, true
		}
	}
	return out, found
}

func yesNo(v float64) string {
	if v >= 0.999 {
		return "yes"
	}
	return fmt.Sprintf("%.2f", v)
}

// BaselinePlanned reports whether this plan runs the skill against a baseline.
// Read from the plan's own run keys rather than from a tier constant, so it
// stays true to what was actually scheduled. Without a baseline, Uplift is
// 0.5 by construction — a tie nothing measured — and printing that as a
// number invites the reader to interpret it as a result.
func BaselinePlanned(p runner.Plan) bool {
	for _, k := range p.Runs {
		if k.Condition == runner.CondBaseline {
			return true
		}
	}
	return false
}
