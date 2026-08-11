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

// Split divides a trigger set into a train half and a held-out test half,
// stratified so each half carries both positive and negative queries.
//
// The winner is selected on the test score, so a split that puts every
// negative on one side chooses a description on half the question. A
// holdout of 0 disables the split and trains on everything.
func Split(set []suite.TriggerQuery, holdout float64, seed int64) (train, test []suite.TriggerQuery) {
	if holdout <= 0 {
		return append([]suite.TriggerQuery(nil), set...), nil
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
// One query held back measures little, but zero measures nothing at all and
// makes the test score identical to the train score.
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
