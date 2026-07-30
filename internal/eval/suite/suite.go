// Package suite drafts an evaluation suite from an approved spec.SkillSpec:
// a set of task packages, each with its own oracle and verifier, plus a
// trigger set used to measure whether a skill fires when it should (and
// stays silent when it shouldn't). Suites are written to and loaded from
// disk in the SkillsBench-compatible layout, and ship with a synthetic
// distractor pack for measuring trigger precision.
package suite

import (
	"math"
	"math/rand"
	"sort"
)

// TaskPkg is one generated evaluation task package.
//
// Kind is one of "happy", "variant", "edge", or "negative-trigger". Split is
// "dev" or "holdout" once Split has been called; it is empty on a freshly
// generated, unsplit task.
type TaskPkg struct {
	ID       string
	Kind     string
	Split    string
	PromptMD string
	EnvFrag  string
	Oracle   string
	Verifier string
}

// TriggerSet is the positive and hard-negative example prompts used to
// measure trigger precision: the skill must fire on every Positive prompt
// and stay silent on every Negative one.
type TriggerSet struct {
	Positive []string `yaml:"positive"`
	Negative []string `yaml:"negative"`
}

// Suite is a drafted evaluation suite: its task packages and trigger set.
type Suite struct {
	Tasks    []TaskPkg
	Triggers TriggerSet
}

// holdoutFraction is the target fraction of tasks held out from the repair
// loop. The reported score is the holdout score.
const holdoutFraction = 0.3

// Split assigns each task to the dev or holdout set, deterministically for a
// given seed: tasks are sorted by ID first so the starting order never
// depends on generation order, then shuffled with a seeded source and the
// first max(1, round(0.3*n)) become holdout.
//
// The max(1, ...) matters: without it, a suite small enough that
// round(0.3*n) is zero would produce an empty holdout, and the reported score
// is the holdout score — no holdout tasks means no reportable result at all.
func (s *Suite) Split(seed int64) {
	n := len(s.Tasks)
	if n == 0 {
		return
	}

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return s.Tasks[order[i]].ID < s.Tasks[order[j]].ID })

	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	holdoutN := int(math.Round(holdoutFraction * float64(n)))
	if holdoutN < 1 {
		holdoutN = 1
	}

	for i, idx := range order {
		if i < holdoutN {
			s.Tasks[idx].Split = "holdout"
		} else {
			s.Tasks[idx].Split = "dev"
		}
	}
}
