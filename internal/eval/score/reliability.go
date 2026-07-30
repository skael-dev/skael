package score

import (
	"errors"
	"fmt"
)

// TaskPasses is one task's outcome across its runs: C passes out of N.
type TaskPasses struct {
	TaskID string
	N      int
	C      int
}

// PassAtK is the unbiased estimator of the probability that k independently
// drawn runs all pass, given c passes observed in n runs: C(c,k) / C(n,k).
//
// Unbiased matters at these sample sizes. The obvious estimator — "did all k of
// the runs I have pass" — discards the information in the runs it does not
// look at and is biased downward at n=2, which is the Full tier. This form is
// the standard pass^k estimator and is exact for the sampling-without-
// replacement question actually being asked.
func PassAtK(n, c, k int) (float64, error) {
	switch {
	case n <= 0:
		return 0, errors.New("score.PassAtK: no runs")
	case k <= 0:
		return 0, errors.New("score.PassAtK: k must be positive")
	case k > n:
		return 0, fmt.Errorf("score.PassAtK: k=%d exceeds n=%d; the tier planned fewer runs than the estimator needs", k, n)
	case c < 0 || c > n:
		return 0, fmt.Errorf("score.PassAtK: c=%d is not in [0,%d]", c, n)
	case c < k:
		return 0, nil
	}
	// Computed as a product of ratios rather than a ratio of factorials: n is
	// small here, but the product form cannot overflow and needs no big.Int.
	p := 1.0
	for i := 0; i < k; i++ {
		p *= float64(c-i) / float64(n-i)
	}
	return p, nil
}

// Reliability is the mean of PassAtK over tasks.
//
// Mean over tasks, not over pooled runs: a task that happened to get more runs
// must not weigh more heavily than one that got fewer, or the score drifts with
// scheduling rather than with the skill.
func Reliability(ts []TaskPasses, k int) (float64, error) {
	if len(ts) == 0 {
		return 0, errors.New("score.Reliability: no tasks measured; an unknown reliability is not a zero one")
	}
	sum := 0.0
	for _, t := range ts {
		p, err := PassAtK(t.N, t.C, k)
		if err != nil {
			return 0, fmt.Errorf("score.Reliability: task %s: %w", t.TaskID, err)
		}
		sum += p
	}
	return sum / float64(len(ts)), nil
}
