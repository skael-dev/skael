package contest

import (
	"sort"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/store"
)

// Reduce turns one candidate's report into the per-task results a verdict is
// computed from, plus what that candidate missed on each task.
//
// Only the primary member's skill-condition runs count. A comparison needs one
// model on both sides, and a panel minimum is not a per-task quantity.
func Reduce(label string, r *report.Report) ([]TaskOutcome, []TaskLoss) {
	if r == nil || len(r.ModelPanel) == 0 {
		return nil, nil
	}
	primary := r.ModelPanel[0].Model

	var outcomes []TaskOutcome
	var losses []TaskLoss
	for _, task := range r.Tasks {
		passes, runs := 0, 0
		for _, c := range task.Conditions {
			if c.Model != primary || c.Condition != runner.CondSkill {
				continue
			}
			passes += c.Passes
			runs += c.Runs
		}
		outcomes = append(outcomes, TaskOutcome{
			Candidate: label, TaskID: task.TaskID, Passes: passes, Runs: runs,
		})
		if miss := missed(task, primary); len(miss.Missed) > 0 {
			miss.Label, miss.TaskID = label, task.TaskID
			losses = append(losses, miss)
		}
	}
	return outcomes, losses
}

// missed collects the expectations the primary member failed on one task, with
// the judge's own evidence. Deduplicated across attempts: the same expectation
// failing three times is one thing to fix, not three.
func missed(task report.TaskReport, primary string) TaskLoss {
	seen := map[string]bool{}
	var loss TaskLoss
	for _, g := range task.Grades {
		if g.Model != primary || g.Condition != store.Condition(runner.CondSkill) {
			continue
		}
		for _, e := range g.Expectations {
			if e.Passed || seen[e.Text] {
				continue
			}
			seen[e.Text] = true
			loss.Missed = append(loss.Missed, e.Text)
			if loss.Evidence == "" {
				loss.Evidence = e.Evidence
			}
		}
	}
	sort.Strings(loss.Missed)
	return loss
}
