// Package repair clusters evaluation failures into fixable defects and
// audits which assertions actually discriminate a skill's effect, so a
// repair loop spends its diff budget on real skill defects rather than on
// restating the same failure three times or rewording a broken verifier.
package repair

import (
	"sort"

	"github.com/skael-dev/skael/internal/eval/suite"
)

// Failure is one observed failure from a single run: a contract violation
// (from drift.Observation), a verifier/runner failure (runner.Outcome), or a
// lint finding (lint.Finding), normalized to a common shape for clustering.
//
// Kind is one of "contract", "verifier", "lint".
type Failure struct {
	Kind   string
	ID     string
	TaskID string
	Model  string
	Detail string
}

// FailureCluster groups failures that share a Kind and ID so a repair prompt
// sees one defect once, not once per run.
//
// Named FailureCluster (not Cluster) because the package also exports a
// function named Cluster that produces these — Go does not allow a type and
// a function to share an identifier at package scope.
type FailureCluster struct {
	Key      string
	Kind     string
	ID       string
	Count    int
	Tasks    []string
	Models   []string
	Examples []string
}

const maxExamples = 3

// Cluster groups fs by Kind+ID, accumulating counts and de-duplicated
// task/model lists and up to three example details. The result is sorted by
// descending count, then ascending key, so identical input always produces
// identical output — a repair prompt that varies between runs makes two
// evaluations incomparable.
func Cluster(fs []Failure) []FailureCluster {
	byKey := make(map[string]*FailureCluster)
	order := make([]string, 0)

	for _, f := range fs {
		key := f.Kind + "\x00" + f.ID
		c, ok := byKey[key]
		if !ok {
			c = &FailureCluster{Key: key, Kind: f.Kind, ID: f.ID}
			byKey[key] = c
			order = append(order, key)
		}
		c.Count++
		c.Tasks = appendUnique(c.Tasks, f.TaskID)
		c.Models = appendUnique(c.Models, f.Model)
		if len(c.Examples) < maxExamples && f.Detail != "" {
			c.Examples = append(c.Examples, f.Detail)
		}
	}

	out := make([]FailureCluster, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func appendUnique(ss []string, s string) []string {
	if s == "" {
		return ss
	}
	for _, existing := range ss {
		if existing == s {
			return ss
		}
	}
	return append(ss, s)
}

// ConditionResult is one panel member's pass/fail outcome for one task,
// under both the skill-present and baseline (skill-absent) conditions.
type ConditionResult struct {
	TaskID         string
	Model          string
	SkillPassed    bool
	BaselinePassed bool
}

// Audit partitions tasks by what their results actually measured.
type Audit struct {
	NonDiscriminating []string
	BrokenInBoth      []string
	Discriminating    []string
}

// AuditAssertions partitions tasks by what their results actually measured.
//
// The three cases lead to three different actions, and confusing them is the
// most expensive mistake a repair loop can make:
//
//   - Passes with the skill and without it: the task measures nothing about the
//     skill. It is pruned, because leaving it in inflates Reliability for both
//     conditions and pulls Uplift toward a tie — a skill looks less useful
//     precisely because some of its tasks were free.
//   - Fails with the skill and without it: the task or its verifier is broken.
//     It goes to the suite. No amount of rewording the skill fixes a verifier
//     that rejects its own oracle, and three iterations trying is three
//     iterations of the budget.
//   - Anything else discriminates and is the repair loop's actual input.
//
// A task counts as non-discriminating only when *every* panel member passed it
// in both conditions. A task the floor model needed the skill for and the strong
// model did not is the RobustnessGap signal, and pruning it would delete the
// evidence that the skill matters where it matters most.
func AuditAssertions(rs []ConditionResult) Audit {
	type agg struct {
		allPassBoth bool
		allFailBoth bool
	}
	byTask := make(map[string]*agg)
	order := make([]string, 0)

	for _, r := range rs {
		a, ok := byTask[r.TaskID]
		if !ok {
			a = &agg{allPassBoth: true, allFailBoth: true}
			byTask[r.TaskID] = a
			order = append(order, r.TaskID)
		}
		passBoth := r.SkillPassed && r.BaselinePassed
		failBoth := !r.SkillPassed && !r.BaselinePassed
		if !passBoth {
			a.allPassBoth = false
		}
		if !failBoth {
			a.allFailBoth = false
		}
	}

	var out Audit
	for _, taskID := range order {
		a := byTask[taskID]
		switch {
		case a.allPassBoth:
			out.NonDiscriminating = append(out.NonDiscriminating, taskID)
		case a.allFailBoth:
			out.BrokenInBoth = append(out.BrokenInBoth, taskID)
		default:
			out.Discriminating = append(out.Discriminating, taskID)
		}
	}
	return out
}

// PruneTasks removes tasks flagged as non-discriminating or broken-in-both
// from ts, so a later scoring pass never counts a pruned check toward
// Reliability or Uplift.
func PruneTasks(a Audit, ts []suite.TaskPkg) []suite.TaskPkg {
	drop := make(map[string]bool, len(a.NonDiscriminating)+len(a.BrokenInBoth))
	for _, id := range a.NonDiscriminating {
		drop[id] = true
	}
	for _, id := range a.BrokenInBoth {
		drop[id] = true
	}

	out := make([]suite.TaskPkg, 0, len(ts))
	for _, task := range ts {
		if drop[task.ID] {
			continue
		}
		out = append(out, task)
	}
	return out
}
