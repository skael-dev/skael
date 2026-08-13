package tune_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/tune"
)

func set(pos, neg int) []suite.TriggerQuery {
	var out []suite.TriggerQuery
	for i := 0; i < pos; i++ {
		out = append(out, suite.TriggerQuery{Query: "p" + string(rune('a'+i)), ShouldTrigger: true})
	}
	for i := 0; i < neg; i++ {
		out = append(out, suite.TriggerQuery{Query: "n" + string(rune('a'+i)), ShouldTrigger: false})
	}
	return out
}

// TestSplit_StratifiesByShouldTrigger pins the property that makes the test
// score mean anything. A split that puts every negative in the test set
// scores a description on half the question.
func TestSplit_StratifiesByShouldTrigger(t *testing.T) {
	train, test := tune.Split(set(8, 8), 0.4, 42)

	count := func(qs []suite.TriggerQuery) (int, int) {
		var p, n int
		for _, q := range qs {
			if q.ShouldTrigger {
				p++
			} else {
				n++
			}
		}
		return p, n
	}

	tp, tn := count(test)
	if tp != 3 || tn != 3 {
		t.Errorf("test split = %d positive, %d negative; want 3 and 3", tp, tn)
	}
	rp, rn := count(train)
	if rp != 5 || rn != 5 {
		t.Errorf("train split = %d positive, %d negative; want 5 and 5", rp, rn)
	}
}

// TestSplit_KeepsAtLeastOneOfEachInTest mirrors max(1, ...) in the Python. A
// tiny set must still hold something back, or the winner is chosen on the
// same queries it was tuned against.
func TestSplit_KeepsAtLeastOneOfEachInTest(t *testing.T) {
	_, test := tune.Split(set(2, 2), 0.4, 42)
	if len(test) != 2 {
		t.Errorf("test split holds %d queries, want 2", len(test))
	}
	var pos, neg int
	for _, q := range test {
		if q.ShouldTrigger {
			pos++
		} else {
			neg++
		}
	}
	if pos != 1 || neg != 1 {
		t.Errorf("test split = %d positive, %d negative; want 1 and 1", pos, neg)
	}
}

func TestSplit_IsStableForOneSeed(t *testing.T) {
	a, b := tune.Split(set(8, 8), 0.4, 42)
	c, d := tune.Split(set(8, 8), 0.4, 42)
	if len(a) != len(c) || len(b) != len(d) {
		t.Fatal("split sizes differ across two calls with one seed")
	}
	for i := range a {
		if a[i].Query != c[i].Query {
			t.Fatalf("train[%d] = %q then %q; the split is not stable", i, a[i].Query, c[i].Query)
		}
	}
	for i := range b {
		if b[i].Query != d[i].Query {
			t.Fatalf("test[%d] = %q then %q; the split is not stable", i, b[i].Query, d[i].Query)
		}
	}
}

// TestSplit_ZeroHoldoutTestsNothing keeps the escape hatch the Python has.
// A holdout of 0 trains on everything. It then selects on the train score.
func TestSplit_ZeroHoldoutTestsNothing(t *testing.T) {
	train, test := tune.Split(set(8, 8), 0, 42)
	if len(train) != 16 || len(test) != 0 {
		t.Errorf("holdout 0 gave %d train and %d test; want 16 and 0", len(train), len(test))
	}
}

// TestSplit_ClampsAHoldoutOfOne covers the setting that measures nothing. An
// empty train half makes Run exit at iteration 1 and report that every train
// query passed, which reads like a result and is the absence of one.
func TestSplit_ClampsAHoldoutOfOne(t *testing.T) {
	for _, holdout := range []float64{1, 1.5} {
		train, test := tune.Split(set(4, 4), holdout, 42)
		if len(train) == 0 {
			t.Errorf("holdout %v left nothing to train on", holdout)
		}
		if len(test) == 0 {
			t.Errorf("holdout %v held nothing out", holdout)
		}
	}
}
