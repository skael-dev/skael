// Package contest decides which of two or more candidate skill versions is
// better, from runs measured side by side.
//
// Decide takes no database, no HTTP, no context and no clock, so every
// question about a verdict is answerable by a table test.
package contest

import (
	"fmt"
	"math"
	"sort"
)

// Outcome is what a contest concluded.
type Outcome string

const (
	// OutcomeWinner names a candidate the sign test separated from the rest.
	OutcomeWinner Outcome = "winner"
	// OutcomeTooClose is the honest result when the gap is inside the noise.
	// It is a verdict, not a failure to reach one.
	OutcomeTooClose Outcome = "too_close_to_call"
)

// Alpha is the two-sided significance the sign test must reach to name a
// winner. A contest runs a dozen tasks, so this is the whole defence against
// calling a coin flip a result.
const Alpha = 0.05

// TaskOutcome is one candidate's measured result on one task.
type TaskOutcome struct {
	Candidate string
	TaskID    string
	Passes    int
	Runs      int
}

// Rate is the candidate's pass rate on this task. A task with no run rates
// zero, and Decide drops it rather than scoring it.
func (t TaskOutcome) Rate() float64 {
	if t.Runs == 0 {
		return 0
	}
	return float64(t.Passes) / float64(t.Runs)
}

// TaskComparison is one task's head-to-head result between two candidates.
type TaskComparison struct {
	TaskID string  `json:"task_id"`
	A      string  `json:"a"`
	B      string  `json:"b"`
	ARate  float64 `json:"a_rate"`
	BRate  float64 `json:"b_rate"`
	// Winner is empty for a tie.
	Winner string `json:"winner"`
}

// Pairing is the full result between two candidates.
type Pairing struct {
	A       string           `json:"a"`
	B       string           `json:"b"`
	AWins   int              `json:"a_wins"`
	BWins   int              `json:"b_wins"`
	Ties    int              `json:"ties"`
	PValue  float64          `json:"p_value"`
	Outcome Outcome          `json:"outcome"`
	Winner  string           `json:"winner"`
	Tasks   []TaskComparison `json:"tasks"`
}

// Verdict is a contest's conclusion over every pair of candidates.
type Verdict struct {
	Outcome  Outcome   `json:"outcome"`
	Winner   string    `json:"winner"`
	Detail   string    `json:"detail"`
	Pairings []Pairing `json:"pairings"`
}

// Decide compares every pair of candidates task by task.
//
// A task is a win, a loss or a tie on pass rate, and the tied tasks are
// dropped before the remainder is tested against a coin. Dropping ties is what
// makes this a sign test rather than a count: a suite where both candidates
// pass everything carries no evidence either way, and treating those tasks as
// agreement would let a long easy suite manufacture significance.
//
// A candidate wins the contest when it beats every other candidate. Anything
// else is too close to call, including a cycle.
func Decide(outcomes []TaskOutcome) (Verdict, error) {
	byCandidate := map[string]map[string]TaskOutcome{}
	names := []string{}
	for _, o := range outcomes {
		if o.Candidate == "" || o.TaskID == "" {
			return Verdict{}, fmt.Errorf("contest.Decide: outcome with empty candidate or task")
		}
		if _, ok := byCandidate[o.Candidate]; !ok {
			byCandidate[o.Candidate] = map[string]TaskOutcome{}
			names = append(names, o.Candidate)
		}
		byCandidate[o.Candidate][o.TaskID] = o
	}
	if len(names) < 2 {
		return Verdict{}, fmt.Errorf("contest.Decide: a contest needs at least two candidates, got %d", len(names))
	}
	sort.Strings(names)

	var pairings []Pairing
	beats := map[string]map[string]bool{}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			p := comparePair(names[i], names[j], byCandidate)
			pairings = append(pairings, p)
			if p.Winner != "" {
				loser := p.A
				if p.Winner == p.A {
					loser = p.B
				}
				if beats[p.Winner] == nil {
					beats[p.Winner] = map[string]bool{}
				}
				beats[p.Winner][loser] = true
			}
		}
	}

	v := Verdict{Outcome: OutcomeTooClose, Pairings: pairings}
	for _, n := range names {
		if len(beats[n]) == len(names)-1 {
			v.Outcome, v.Winner = OutcomeWinner, n
			break
		}
	}
	v.Detail = describe(v, names)
	return v, nil
}

func comparePair(a, b string, byCandidate map[string]map[string]TaskOutcome) Pairing {
	p := Pairing{A: a, B: b, Outcome: OutcomeTooClose}

	taskIDs := []string{}
	for id := range byCandidate[a] {
		if _, ok := byCandidate[b][id]; ok {
			taskIDs = append(taskIDs, id)
		}
	}
	sort.Strings(taskIDs)

	for _, id := range taskIDs {
		ao, bo := byCandidate[a][id], byCandidate[b][id]
		// A task nobody ran carries no evidence. It is not a tie between two
		// candidates, it is an absence, and counting it as a tie would inflate
		// the tie column with tasks that never happened.
		if ao.Runs == 0 || bo.Runs == 0 {
			continue
		}
		tc := TaskComparison{TaskID: id, A: a, B: b, ARate: ao.Rate(), BRate: bo.Rate()}
		switch {
		case ao.Rate() > bo.Rate():
			tc.Winner = a
			p.AWins++
		case bo.Rate() > ao.Rate():
			tc.Winner = b
			p.BWins++
		default:
			p.Ties++
		}
		p.Tasks = append(p.Tasks, tc)
	}

	p.PValue = signTest(p.AWins, p.BWins)
	if p.PValue <= Alpha && p.AWins != p.BWins {
		p.Outcome = OutcomeWinner
		p.Winner = a
		if p.BWins > p.AWins {
			p.Winner = b
		}
	}
	return p
}

// signTest is the two-sided exact binomial probability of a split at least
// this lopsided, if the candidates were equal. Exact rather than normal: at a
// dozen tasks every asymptotic approximation is wrong in the direction that
// finds results.
func signTest(aWins, bWins int) float64 {
	n := aWins + bWins
	if n == 0 {
		return 1
	}
	k := aWins
	if bWins > aWins {
		k = bWins
	}

	// P(X >= k) under p=0.5, doubled for the other tail.
	tail := 0.0
	for i := k; i <= n; i++ {
		tail += binomial(n, i)
	}
	p := 2 * tail / math.Pow(2, float64(n))
	return math.Min(p, 1)
}

func binomial(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	// Multiplicative form, so a 20-task contest does not overflow a factorial.
	c := 1.0
	for i := 0; i < k; i++ {
		c = c * float64(n-i) / float64(i+1)
	}
	return c
}

// MinDecidedTasks is the fewest tasks that can separate two candidates at all:
// below it, even a clean sweep is inside what a coin does. A contest that
// returns fewer than this has not measured a close result, it has measured
// nothing, and the fix is more discriminating tasks rather than more attempts.
func MinDecidedTasks() int {
	for n := 1; n <= 64; n++ {
		if 2/math.Pow(2, float64(n)) <= Alpha {
			return n
		}
	}
	return 64
}

func describe(v Verdict, names []string) string {
	if v.Outcome == OutcomeWinner {
		return fmt.Sprintf("%s wins", v.Winner)
	}
	if len(names) > 2 {
		return "too close to call: no candidate beat every other"
	}

	p := v.Pairings[0]
	decided := p.AWins + p.BWins
	if decided < MinDecidedTasks() {
		return fmt.Sprintf(
			"too close to call: only %d of %d tasks separated the candidates, and %d are needed before any split can beat chance — the suite needs tasks these candidates handle differently",
			decided, decided+p.Ties, MinDecidedTasks())
	}
	return fmt.Sprintf("too close to call: %d-%d over %d decided tasks is within noise", p.AWins, p.BWins, decided)
}
