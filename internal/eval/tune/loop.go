package tune

import (
	"context"
	"fmt"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// Options configures one tuning run.
type Options struct {
	SkillName   string
	SkillBody   string
	Description string
	Queries     []suite.TriggerQuery
	Distractors []suite.Distractor
	Runs        int
	Iterations  int
	Concurrency int
	Threshold   float64
	Holdout     float64
	Seed        int64
	// Log reports progress. Nil discards it.
	Log func(format string, args ...any)
}

// Result is what a tuning run produced.
type Result struct {
	Original   string    `json:"original_description"`
	Best       string    `json:"best_description"`
	Final      string    `json:"final_description"`
	BestTrain  string    `json:"best_train_score"`
	BestTest   string    `json:"best_test_score,omitempty"`
	Iterations int       `json:"iterations_run"`
	History    []Attempt `json:"history"`
	ExitReason string    `json:"exit_reason"`
}

// Run tunes a description against a trigger set.
//
// Each iteration scores the current description on both halves, then proposes
// a new one from the train failures alone. The winner is the iteration with
// the best score on the held-out half, not the best train score. A
// description fitted to the queries that tuned it is exactly what the split
// exists to reject.
func Run(ctx context.Context, g llm.Gateway, opts Options) (Result, error) {
	if g == nil {
		return Result{}, fmt.Errorf("tune: Run requires a gateway")
	}
	if len(opts.Queries) == 0 {
		return Result{}, fmt.Errorf("tune: %s has no trigger queries to tune against", opts.SkillName)
	}
	if opts.Iterations < 1 {
		opts.Iterations = 1
	}
	if opts.Seed == 0 {
		opts.Seed = 42
	}
	logf := opts.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	train, test := Split(opts.Queries, opts.Holdout, opts.Seed)
	logf("split %d train and %d held out", len(train), len(test))

	scoreOpts := ScoreOptions{
		Distractors: opts.Distractors, Runs: opts.Runs,
		Threshold: opts.Threshold, Concurrency: opts.Concurrency,
	}

	current := opts.Description
	res := Result{Original: opts.Description}

	for i := 1; i <= opts.Iterations; i++ {
		trainScore, err := Score(ctx, g, opts.SkillName, current, train, scoreOpts)
		if err != nil {
			return Result{}, err
		}
		attempt := Attempt{Iteration: i, Description: current, Train: trainScore}
		if len(test) > 0 {
			testScore, terr := Score(ctx, g, opts.SkillName, current, test, scoreOpts)
			if terr != nil {
				return Result{}, terr
			}
			attempt.Test, attempt.TestMeasured = testScore, true
		}
		res.History = append(res.History, attempt)
		logf("iteration %d: train %d/%d", i, trainScore.Passed, trainScore.Total)

		if trainScore.Failed == 0 {
			res.ExitReason = fmt.Sprintf("every train query passed at iteration %d", i)
			break
		}
		if i == opts.Iterations {
			res.ExitReason = fmt.Sprintf("reached the iteration limit of %d", opts.Iterations)
			break
		}

		next, err := Improve(ctx, g, opts.SkillName, opts.SkillBody, current, trainScore, res.History)
		if err != nil {
			return Result{}, err
		}
		current = next
	}

	best := res.History[0]
	for _, a := range res.History[1:] {
		if better(a, best) {
			best = a
		}
	}
	res.Best = best.Description
	res.Final = current
	res.Iterations = len(res.History)
	res.BestTrain = fmt.Sprintf("%d/%d", best.Train.Passed, best.Train.Total)
	if best.TestMeasured {
		res.BestTest = fmt.Sprintf("%d/%d", best.Test.Passed, best.Test.Total)
	}
	return res, nil
}

// better ranks two attempts. The held-out score decides. The train score
// breaks a tie. With no held-out half there is nothing but the train score.
func better(a, b Attempt) bool {
	if a.TestMeasured && b.TestMeasured {
		if a.Test.Passed != b.Test.Passed {
			return a.Test.Passed > b.Test.Passed
		}
	}
	return a.Train.Passed > b.Train.Passed
}
