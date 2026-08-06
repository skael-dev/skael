package drift_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/drift"
)

// A run whose checks mostly could not be performed must not report a number:
// the components break in both directions at once (coverage to zero, violation
// and order to a vacuous 1.0), leaving a constant fixed by the weights that
// reads exactly like a measurement.
func TestScore_MostlyUnevaluableRunIsUnmeasurable(t *testing.T) {
	tests := []struct {
		name                   string
		attempted, unevaluable int
		want                   bool
	}{
		{name: "nothing unevaluable", attempted: 10, want: false},
		{name: "a few unevaluable is ordinary", attempted: 10, unevaluable: 2, want: false},
		{name: "exactly at the threshold is still measurable", attempted: 10, unevaluable: 5, want: false},
		{name: "past the threshold", attempted: 10, unevaluable: 6, want: true},
		{name: "every check failed", attempted: 10, unevaluable: 10, want: true},
		// A purely semantic contract attempts no path checks at all. That is a
		// legitimate shape, not a broken run.
		{name: "nothing attempted is measurable", attempted: 0, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &drift.Observation{
				Total:       10,
				Attempted:   tc.attempted,
				Unevaluable: tc.unevaluable,
			}
			r, err := drift.Score(o, 0.5, drift.DefaultWeights)
			if err != nil {
				t.Fatal(err)
			}
			if r.Unmeasurable != tc.want {
				t.Errorf("Unmeasurable = %v, want %v (%d/%d unevaluable)",
					r.Unmeasurable, tc.want, tc.unevaluable, tc.attempted)
			}
		})
	}
}

// An unmeasurable run must not drag its measurable siblings toward the
// constant that unchecked components produce.
func TestAggregate_DropsUnmeasurableRuns(t *testing.T) {
	good := drift.Result{Adherence: 90}
	bad := drift.Result{Adherence: 40, Unmeasurable: true}

	agg, err := drift.Aggregate([]drift.Result{good, bad, good})
	if err != nil {
		t.Fatal(err)
	}
	if agg.N != 2 {
		t.Errorf("N = %d, want 2: the unmeasurable run should not be counted", agg.N)
	}
	if agg.Mean != 90 {
		t.Errorf("Mean = %v, want 90: an unmeasurable run pulled the mean", agg.Mean)
	}
}

// If nothing was measurable there is nothing to summarise, and that must be an
// error rather than a confident zero — the same rule Aggregate already applies
// to having no runs at all.
func TestAggregate_AllUnmeasurableIsAnError(t *testing.T) {
	_, err := drift.Aggregate([]drift.Result{
		{Adherence: 40, Unmeasurable: true},
		{Adherence: 40, Unmeasurable: true},
	})
	if err == nil {
		t.Fatal("aggregating only unmeasurable runs returned a summary")
	}
	if !strings.Contains(err.Error(), "unmeasurable") {
		t.Errorf("error does not say why: %v", err)
	}
}
