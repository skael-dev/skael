// Package tune optimizes a skill's description for trigger accuracy. It is a
// port of skill-creator's run_loop.py and improve_description.py, with one
// substitution. Every model call goes through internal/eval/llm rather than a
// claude -p subprocess, because internal/eval/provider is the one place the
// environment becomes an LLM backend.
package tune

import (
	"math/rand"

	"github.com/skael-dev/skael/internal/eval/suite"
)

// maxHoldout is the largest fraction that still leaves a train half. At 0.9
// a set of ten holds back nine and trains on one, which is a bad split. At
// 1.0 it trains on nothing at all, which is not a split.
const maxHoldout = 0.9

// Split divides a trigger set into a train half and a held-out test half,
// stratified so each half carries both positive and negative queries.
//
// The winner is selected on the test score, so a split that puts every
// negative on one side chooses a description on half the question. A
// holdout of 0 disables the split. The loop then trains on everything.
//
// A holdout at or above 1 is clamped to maxHoldout. It would otherwise empty
// the train half, and Run exits at iteration 1 reporting that every train
// query passed. That reads like a measurement and is the absence of one.
func Split(set []suite.TriggerQuery, holdout float64, seed int64) (train, test []suite.TriggerQuery) {
	if holdout <= 0 {
		return append([]suite.TriggerQuery(nil), set...), nil
	}
	if holdout > maxHoldout {
		holdout = maxHoldout
	}

	var positive, negative []suite.TriggerQuery
	for _, q := range set {
		if q.ShouldTrigger {
			positive = append(positive, q)
		} else {
			negative = append(negative, q)
		}
	}

	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(positive), func(i, j int) { positive[i], positive[j] = positive[j], positive[i] })
	r.Shuffle(len(negative), func(i, j int) { negative[i], negative[j] = negative[j], negative[i] })

	np := holdoutCount(len(positive), holdout)
	nn := holdoutCount(len(negative), holdout)

	test = append(test, positive[:np]...)
	test = append(test, negative[:nn]...)
	train = append(train, positive[np:]...)
	train = append(train, negative[nn:]...)
	return train, test
}

// holdoutCount is max(1, int(n*holdout)) when there is anything to hold back.
// One query held back measures little. Zero measures nothing at all. Zero
// also makes the test score identical to the train score.
func holdoutCount(n int, holdout float64) int {
	if n == 0 {
		return 0
	}
	c := int(float64(n) * holdout)
	if c < 1 {
		c = 1
	}
	if c > n {
		c = n
	}
	return c
}
