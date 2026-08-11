package tune_test

import (
	"context"
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

// TestImprove_NamesBothFailureKinds pins that the model is shown what missed
// and what fired wrongly. A prompt that showed only one tunes precision or
// recall alone.
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
	if strings.Contains(prompt, "5/6") || strings.Contains(strings.ToLower(prompt), "test") {
		t.Errorf("the improvement prompt leaks the held-out score:\n%s", prompt)
	}
	if !strings.Contains(prompt, "7/10") {
		t.Error("the improvement prompt does not carry the train score")
	}
}

// TestImprove_ShortensADescriptionOverTheLimit covers the second call the
// Python makes. 1024 characters is the Agent Skills limit. A longer one is
// truncated where nobody sees it happen.
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
