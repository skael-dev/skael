package score

import (
	"errors"
	"math/rand"
	"sort"
)

// Bootstrap computes a 95% percentile bootstrap confidence interval for the
// mean of samples, by resampling with replacement iters times and taking the
// 2.5th and 97.5th percentiles of the resampled means.
//
// The source is seeded explicitly with math/rand.New(rand.NewSource(seed))
// rather than a shared global generator, so that the same samples, iters, and
// seed always produce the same interval — a confidence interval that changes
// between two readings of the same data is not one anyone can quote.
func Bootstrap(samples []float64, iters int, seed int64) (lo, hi float64, err error) {
	if len(samples) == 0 {
		return 0, 0, errors.New("score.Bootstrap: no samples")
	}
	if iters <= 0 {
		return 0, 0, errors.New("score.Bootstrap: iters must be positive")
	}

	rng := rand.New(rand.NewSource(seed))
	n := len(samples)
	means := make([]float64, iters)
	for i := 0; i < iters; i++ {
		var sum float64
		for j := 0; j < n; j++ {
			sum += samples[rng.Intn(n)]
		}
		means[i] = sum / float64(n)
	}
	sort.Float64s(means)

	loIdx := int(0.025 * float64(iters))
	hiIdx := int(0.975 * float64(iters))
	if hiIdx >= iters {
		hiIdx = iters - 1
	}
	return means[loIdx], means[hiIdx], nil
}

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
