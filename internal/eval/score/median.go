package score

import (
	"errors"
	"sort"
)

// Median returns the median of xs, without mutating the caller's slice.
// Zero samples is an error, not a value: an absent measurement must not be
// indistinguishable from a real one.
func Median(xs []float64) (float64, error) {
	if len(xs) == 0 {
		return 0, errors.New("score.Median: no samples")
	}
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	sort.Float64s(sorted)

	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2], nil
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2, nil
}

// MedianTokens is the median total token spend across a set of sessions.
// Reported beside the score rather than inside it: a verbose skill that works
// still works, and nobody notices a skill tripling its own token bill unless
// the figure is on the report.
func MedianTokens(totals []int64) (int64, error) {
	if len(totals) == 0 {
		return 0, errors.New("score.MedianTokens: no samples")
	}
	xs := make([]float64, len(totals))
	for i, t := range totals {
		xs[i] = float64(t)
	}
	m, err := Median(xs)
	if err != nil {
		return 0, err
	}
	return int64(m), nil
}
