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
