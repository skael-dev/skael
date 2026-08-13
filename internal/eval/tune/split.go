// Package tune optimizes a skill's description for trigger accuracy.
package tune

import (
	"math/rand"

	"github.com/skael-dev/skael/internal/eval/suite"
)

// maxHoldout caps the fraction so a train half always remains.
const maxHoldout = 0.9

// Split divides a trigger set into train and held-out test halves, stratified
// by positive/negative. A holdout of 0 disables the split; values at or above
// 1 are clamped to maxHoldout.
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

// holdoutCount is max(1, int(n*holdout)) when n > 0.
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
