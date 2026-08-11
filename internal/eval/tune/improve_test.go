package tune_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/tune"
)

func failing() tune.ScoreResult {
	return tune.ScoreResult{
		Total: 2, Passed: 0, Failed: 2,
		Results: []tune.QueryResult{
			{Query: "pull the tables out of this pdf", ShouldTrigger: true, Triggers: 0, Runs: 2},
			{Query: "dedupe this csv", ShouldTrigger: false, Triggers: 2, Runs: 2},
		},
	}
}

// TestImprove_NamesBothFailureKinds pins that the prompt shows the model what
// missed and what fired wrongly. A prompt that shows only one half tunes
// precision or recall alone.
func TestImprove_NamesBothFailureKinds(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) {
		return `{"description":"A better description."}`, nil
	})

	if _, err := tune.Improve(context.Background(), g, "pdf-extract", "body", "old", failing(), nil); err != nil {
		t.Fatal(err)
	}

	prompt := g.Calls()[0].Prompt
	for _, want := range []string{"pull the tables out of this pdf", "dedupe this csv"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not carry the failing query %q", want)
		}
	}
	if len(g.Calls()) != 1 {
		t.Errorf("made %d calls, want 1: an under-limit description needs no shorten retry", len(g.Calls()))
	}
}

// TestImprove_TheHistoryCarriesNoTestScore is the anti-overfit guarantee. A
// model that sees the held-out score tunes against it. The held-out score
// then no longer stays held out.
func TestImprove_TheHistoryCarriesNoTestScore(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) {
		return `{"description":"A better description."}`, nil
	})

	history := []tune.Attempt{{
		Iteration: 1, Description: "old",
		Train:        tune.ScoreResult{Total: 10, Passed: 7, Failed: 3},
		Test:         tune.ScoreResult{Total: 6, Passed: 5, Failed: 1},
		TestMeasured: true,
	}}
	if _, err := tune.Improve(context.Background(), g, "pdf-extract", "body", "old", failing(), history); err != nil {
		t.Fatal(err)
	}

	prompt := g.Calls()[0].Prompt
	heldOut := fmt.Sprintf("%d/%d", history[0].Test.Passed, history[0].Test.Total)
	if strings.Contains(prompt, heldOut) {
		t.Errorf("the improvement prompt leaks the held-out score %q:\n%s", heldOut, prompt)
	}
	trainScore := fmt.Sprintf("%d/%d", history[0].Train.Passed, history[0].Train.Total)
	if !strings.Contains(prompt, trainScore) {
		t.Errorf("the improvement prompt does not carry the train score %q", trainScore)
	}
}

// TestImprove_ShortensADescriptionOverTheLimit covers the second call the
// Python makes. 1024 characters is the Agent Skills limit. Nothing warns a
// person when a longer description loses its tail.
func TestImprove_ShortensADescriptionOverTheLimit(t *testing.T) {
	long := strings.Repeat("x", 1100)
	var calls int
	g := fake.NewFunc(func(llm.Req) (string, error) {
		calls++
		if calls == 1 {
			return `{"description":"` + long + `"}`, nil
		}
		return `{"description":"short enough"}`, nil
	})

	got, err := tune.Improve(context.Background(), g, "pdf-extract", "body", "old", failing(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "short enough" {
		t.Errorf("Improve returned %d characters, want the shortened rewrite", len(got))
	}
	if calls != 2 {
		t.Errorf("made %d calls, want 2: the shorten retry did not run", calls)
	}
}
