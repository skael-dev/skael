package tune_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/tune"
)

// TestRun_SelectsTheWinnerByTestScore is the anti-overfit guarantee. It is
// the reason the loop exists in this shape. A description that scored best on
// the queries that tuned it is a description fitted to them.
//
// The two descriptions must disagree in opposite directions on the two
// halves. A fake that answers the same way for both gives them equal scores.
// The assertion then passes whatever the loop chooses. So the test asks
// Split for the same halves the loop uses. It drives the fake from that.
func TestRun_SelectsTheWinnerByTestScore(t *testing.T) {
	const (
		skill = "pdf-extract"
		seed  = int64(42)
	)
	queries := set(8, 8)
	train, _ := tune.Split(queries, 0.4, seed)

	inTrain := map[string]bool{}
	for _, q := range train {
		inTrain[q.Query] = true
	}
	positive := map[string]bool{}
	for _, q := range queries {
		positive[q.Query] = q.ShouldTrigger
	}
	// One train query always fails under "first". Without it, "first" scores
	// a clean train half. The loop then exits at iteration 1. It never
	// proposes a second description to choose between.
	stumble := train[0].Query

	g := fake.NewFunc(func(r llm.Req) (string, error) {
		if r.Role == "tune.improve" {
			return `{"description":"second"}`, nil
		}
		// The anchor is the query section only, not the whole prompt. The
		// skill name "pdf-extract" contains "pd", one of the two-letter
		// candidates. A plain Contains against the full prompt can then match
		// the skill name instead of the query. Map iteration order is
		// randomized, so this mismatch is unpredictable. selectPrompt always
		// surrounds the query with a blank line on both sides. The skill
		// listing line carries only a single line break.
		var q string
		for candidate := range positive {
			if strings.Contains(r.Prompt, "\n\n"+candidate+"\n\n") {
				q = candidate
				break
			}
		}
		if q == "" {
			return "", fmt.Errorf("the prompt carries no known query")
		}

		// "first" answers the train half correctly. It answers the held-out
		// half wrongly. "second" does the opposite. So "first" wins the half
		// that tunes. "second" wins the half that decides.
		usesFirst := strings.Contains(r.Prompt, skill+": first")
		pass := usesFirst == inTrain[q]
		if usesFirst && q == stumble {
			pass = false
		}

		fire := positive[q] == pass
		if fire {
			return `{"skill":"` + skill + `"}`, nil
		}
		return `{"skill":"none"}`, nil
	})

	res, err := tune.Run(context.Background(), g, tune.Options{
		SkillName: skill, SkillBody: "body", Description: "first",
		Queries:    queries,
		Runs:       1,
		Iterations: 2,
		Threshold:  0.5,
		Holdout:    0.4,
		Seed:       seed,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.History) != 2 {
		t.Fatalf("ran %d iterations, want 2; the loop never proposed a second description", len(res.History))
	}
	// Guard the fixture itself. If these two scores ever match, the test has
	// stopped measuring anything and must be repaired rather than trusted.
	if res.History[0].Train.Passed <= res.History[1].Train.Passed {
		t.Fatalf("the fixture is broken: %q did not win the train half", res.History[0].Description)
	}
	if res.History[0].Test.Passed >= res.History[1].Test.Passed {
		t.Fatalf("the fixture is broken: %q did not win the held-out half", res.History[1].Description)
	}

	if res.Best != "second" {
		t.Errorf("Run chose %q, which won the train half and lost the held-out half", res.Best)
	}
}

// TestRun_StopsWhenTheTrainSetIsClean covers the early exit. Another
// iteration on a description that fails nothing buys nothing.
func TestRun_StopsWhenTheTrainSetIsClean(t *testing.T) {
	var improves int
	g := fake.NewFunc(func(r llm.Req) (string, error) {
		if r.Role == "tune.improve" {
			improves++
			return `{"description":"never reached"}`, nil
		}
		if strings.Contains(r.Prompt, "should fire") {
			return `{"skill":"pdf-extract"}`, nil
		}
		return `{"skill":"none"}`, nil
	})

	res, err := tune.Run(context.Background(), g, tune.Options{
		SkillName: "pdf-extract", SkillBody: "body", Description: "d",
		Queries: []suite.TriggerQuery{
			{Query: "should fire a", ShouldTrigger: true},
			{Query: "should fire b", ShouldTrigger: true},
			{Query: "quiet a", ShouldTrigger: false},
			{Query: "quiet b", ShouldTrigger: false},
		},
		Runs: 1, Iterations: 3, Threshold: 0.5, Holdout: 0.4, Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if improves != 0 {
		t.Errorf("made %d improvement calls on a clean train set, want 0", improves)
	}
	if res.Iterations != 1 {
		t.Errorf("ran %d iterations on a clean train set, want 1", res.Iterations)
	}
}

func TestRun_RefusesAnEmptyQuerySet(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return `{"skill":"none"}`, nil })
	if _, err := tune.Run(context.Background(), g, tune.Options{
		SkillName: "pdf-extract", Description: "d", Runs: 1, Iterations: 1,
	}); err == nil {
		t.Error("Run accepted an empty query set")
	}
}
