package score_test

import (
	"math"
	"testing"

	"github.com/skael-dev/skael/internal/eval/score"
)

func TestPassAtK_KnownValues(t *testing.T) {
	cases := []struct {
		n, c, k int
		want    float64
	}{
		{2, 2, 2, 1}, // both runs passed: certainly pass^2
		{2, 1, 2, 0}, // one pass cannot yield two
		{2, 0, 2, 0},
		{4, 3, 3, 1.0 / 4}, // C(3,3)/C(4,3)
		{4, 4, 3, 1},
		{4, 2, 3, 0},
		{1, 1, 1, 1},
		{5, 3, 2, 0.3}, // C(3,2)/C(5,2) = 3/10
	}
	for _, c := range cases {
		got, err := score.PassAtK(c.n, c.c, c.k)
		if err != nil {
			t.Fatalf("PassAtK(%d,%d,%d): %v", c.n, c.c, c.k, err)
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("PassAtK(%d,%d,%d) = %v, want %v", c.n, c.c, c.k, got, c.want)
		}
	}
}

func TestPassAtK_RejectsImpossibleInput(t *testing.T) {
	for _, c := range [][3]int{{2, 3, 2}, {2, 2, 3}, {0, 0, 1}, {2, -1, 1}, {2, 2, 0}} {
		if _, err := score.PassAtK(c[0], c[1], c[2]); err == nil {
			t.Errorf("PassAtK(%d,%d,%d) returned a value", c[0], c[1], c[2])
		}
	}
}

func TestReliability_AveragesOverTasks(t *testing.T) {
	got, err := score.Reliability([]score.TaskPasses{
		{TaskID: "t1", N: 2, C: 2},
		{TaskID: "t2", N: 2, C: 1},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	// One task certainly reliable, one certainly not: 0.5. Averaging over tasks
	// rather than pooling runs keeps a task with more runs from dominating.
	if math.Abs(got-0.5) > 1e-9 {
		t.Errorf("Reliability = %v, want 0.5", got)
	}
}

func TestReliability_ZeroTasksIsAnError(t *testing.T) {
	// A skill with no measured tasks has an unknown reliability. Returning 0
	// makes it indistinguishable from one that failed every task, and
	// Effectiveness is a geometric mean — a zero here zeroes the whole score.
	if _, err := score.Reliability(nil, 2); err == nil {
		t.Error("Reliability returned a value for zero tasks")
	}
}
