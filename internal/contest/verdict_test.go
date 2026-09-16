package contest_test

import (
	"math"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/contest"
)

// tasks builds one candidate's outcomes from a pass count per task, at a fixed
// three attempts each.
func tasks(candidate string, passes ...int) []contest.TaskOutcome {
	out := make([]contest.TaskOutcome, 0, len(passes))
	for i, p := range passes {
		out = append(out, contest.TaskOutcome{
			Candidate: candidate,
			TaskID:    string(rune('a' + i)),
			Passes:    p,
			Runs:      3,
		})
	}
	return out
}

func decide(t *testing.T, outcomes ...[]contest.TaskOutcome) contest.Verdict {
	t.Helper()
	var all []contest.TaskOutcome
	for _, o := range outcomes {
		all = append(all, o...)
	}
	v, err := contest.Decide(all)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return v
}

// A majority is not a result. 8-1 clears the bar; 7-2 is p=0.18 and does not,
// which is exactly the split a reader calls decisive by eye.
func TestDecide_SeparatesALopsidedSplitFromANarrowOne(t *testing.T) {
	lopsided := decide(t,
		tasks("a", 3, 3, 3, 3, 3, 3, 3, 3, 0),
		tasks("b", 0, 0, 0, 0, 0, 0, 0, 0, 3),
	)
	if lopsided.Outcome != contest.OutcomeWinner || lopsided.Winner != "a" {
		t.Errorf("8-1 gave %s/%q, want a winner", lopsided.Outcome, lopsided.Winner)
	}

	narrow := decide(t,
		tasks("a", 3, 3, 3, 3, 3, 3, 3, 0, 0),
		tasks("b", 0, 0, 0, 0, 0, 0, 0, 3, 3),
	)
	if narrow.Outcome != contest.OutcomeTooClose {
		t.Errorf("7-2 gave %s, want too close to call", narrow.Outcome)
	}
}

// A contest that decided too few tasks has measured nothing, and says so
// rather than reporting a close result.
func TestDecide_NamesTheSuiteWhenTooFewTasksSeparateTheCandidates(t *testing.T) {
	v := decide(t,
		tasks("a", 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 0),
		tasks("b", 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3),
	)
	if v.Outcome != contest.OutcomeTooClose {
		t.Fatalf("outcome = %s, want too close to call", v.Outcome)
	}
	if !strings.Contains(v.Detail, "the suite needs tasks") {
		t.Errorf("detail = %q, want it to name the suite as the problem", v.Detail)
	}
}

// Two identical candidates must never produce a winner, however long the suite.
func TestDecide_IdenticalCandidatesAreTooCloseToCall(t *testing.T) {
	v := decide(t,
		tasks("a", 3, 3, 2, 1, 0, 3, 3, 2, 1, 0, 3, 3),
		tasks("b", 3, 3, 2, 1, 0, 3, 3, 2, 1, 0, 3, 3),
	)
	if v.Outcome != contest.OutcomeTooClose {
		t.Fatalf("outcome = %s, want too close to call", v.Outcome)
	}
	p := v.Pairings[0]
	if p.Ties != 12 || p.AWins != 0 || p.BWins != 0 {
		t.Errorf("got %d-%d with %d ties, want 0-0 with 12 ties", p.AWins, p.BWins, p.Ties)
	}
	if p.PValue != 1 {
		t.Errorf("p = %v, want 1", p.PValue)
	}
}

// A suite both candidates pass completely carries no evidence. Counting the
// tied tasks as agreement would let a long easy suite manufacture a result.
func TestDecide_TiedTasksAreDroppedNotCounted(t *testing.T) {
	v := decide(t,
		tasks("a", 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 0),
		tasks("b", 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3),
	)
	p := v.Pairings[0]
	if p.Ties != 11 || p.BWins != 1 || p.AWins != 0 {
		t.Fatalf("got %d-%d with %d ties, want 0-1 with 11 ties", p.AWins, p.BWins, p.Ties)
	}
	if v.Outcome != contest.OutcomeTooClose {
		t.Errorf("one win out of twelve tasks gave %s, want too close to call", v.Outcome)
	}
}

// A task nobody ran is an absence, not agreement.
func TestDecide_ATaskWithNoRunIsNotATie(t *testing.T) {
	a := tasks("a", 3, 3)
	b := tasks("b", 0, 0)
	a = append(a, contest.TaskOutcome{Candidate: "a", TaskID: "z", Passes: 0, Runs: 0})
	b = append(b, contest.TaskOutcome{Candidate: "b", TaskID: "z", Passes: 0, Runs: 0})

	p := decide(t, a, b).Pairings[0]
	if p.Ties != 0 || len(p.Tasks) != 2 {
		t.Errorf("got %d ties over %d tasks, want 0 ties over 2", p.Ties, len(p.Tasks))
	}
}

// Three candidates where nobody beats everyone is too close to call, not a
// winner picked from a cycle.
func TestDecide_AWinnerMustBeatEveryOtherCandidate(t *testing.T) {
	v := decide(t,
		tasks("a", 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0),
		tasks("b", 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3),
		tasks("c", 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3),
	)
	if v.Outcome != contest.OutcomeWinner || v.Winner != "c" {
		t.Fatalf("outcome %s winner %q, want c to win", v.Outcome, v.Winner)
	}
	if len(v.Pairings) != 3 {
		t.Errorf("%d pairings, want 3 for three candidates", len(v.Pairings))
	}
}

// The p-value is the exact binomial, not an approximation. Nine wins to zero
// under a fair coin is 2/2^9.
func TestDecide_PValueIsExact(t *testing.T) {
	p := decide(t,
		tasks("a", 3, 3, 3, 3, 3, 3, 3, 3, 3),
		tasks("b", 0, 0, 0, 0, 0, 0, 0, 0, 0),
	).Pairings[0]

	want := 2.0 / math.Pow(2, 9)
	if math.Abs(p.PValue-want) > 1e-12 {
		t.Errorf("p = %v, want %v", p.PValue, want)
	}
}

func TestDecide_RefusesFewerThanTwoCandidates(t *testing.T) {
	if _, err := contest.Decide(tasks("a", 3, 3)); err == nil {
		t.Fatal("Decide accepted a single candidate")
	}
}

// The table reads in the suite's order. Sorting by id puts "task-10" before
// "task-2", which reads as noise to anyone checking a disputed result.
func TestDecide_KeepsTheSuiteOrder(t *testing.T) {
	ids := []string{"task-1", "task-2", "task-10", "task-11"}
	var all []contest.TaskOutcome
	for _, cand := range []string{"a", "b"} {
		for _, id := range ids {
			all = append(all, contest.TaskOutcome{Candidate: cand, TaskID: id, Passes: 3, Runs: 3})
		}
	}
	v, err := contest.Decide(all)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	var got []string
	for _, task := range v.Pairings[0].Tasks {
		got = append(got, task.TaskID)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("task order = %v, want %v", got, ids)
		}
	}
}
