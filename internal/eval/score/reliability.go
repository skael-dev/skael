package score

import (
	"errors"
	"fmt"
)

// TaskPasses is one task's outcome across its runs: C passes out of N.
//
// Errored counts runs that could not be measured at all — a rate-limit
// exhaustion or an agent-reported internal error, store.StatusError in the
// runner's terms — and is deliberately excluded from N: an errored run is
// neither a pass nor a fail, the same reasoning score.Probe.Unknown applies
// on the trigger path so infrastructure failures don't masquerade as
// reliability failures. A task whose runs all errored (N==0, Errored>0) has
// produced no measurement whatsoever; callers should treat it the way
// report.VoidTask already treats a task excluded from scoring — dropped from
// the denominator and listed separately — rather than as a task that failed
// every run.
type TaskPasses struct {
	TaskID  string
	N       int
	C       int
	Errored int
}

// Void reports whether t produced no measurement at all: every run errored,
// so there is nothing to compute a pass rate from. A caller building the
// []TaskPasses fed to Reliability must exclude such a task from that slice —
// the same way report.Compose excludes a report.VoidTask from the tasks it
// scores — rather than let it reach PassAtK, which refuses N==0.
func (t TaskPasses) Void() bool { return t.N == 0 && t.Errored > 0 }

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
