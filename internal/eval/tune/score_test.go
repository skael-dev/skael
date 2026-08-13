package tune_test

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/tune"
)

var pack = []suite.Distractor{
	{Name: "csv-clean", Description: "Clean up CSV files.", Tier: "near"},
	{Name: "haiku-writer", Description: "Write haiku.", Tier: "far"},
}

// TestScore_PassesWhenTheRateReachesTheThreshold covers a positive query that
// the model selects the skill for.
func TestScore_PassesWhenTheRateReachesTheThreshold(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) {
		return `{"skill":"pdf-extract"}`, nil
	})

	res, err := tune.Score(context.Background(), g, "pdf-extract", "Extracts tables from PDFs.",
		[]suite.TriggerQuery{{Query: "pull the tables out of this pdf", ShouldTrigger: true}},
		tune.ScoreOptions{Distractors: pack, Runs: 2, Threshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 || res.Results[0].Triggers != 2 {
		t.Errorf("got %d passed, %d triggers; want 1 and 2", res.Passed, res.Results[0].Triggers)
	}
}

// TestScore_ANegativeQueryPassesWhenTheSkillStaysSilent is the precision half.
// The distractor pack exists so that this can fail for a real reason.
func TestScore_ANegativeQueryPassesWhenTheSkillStaysSilent(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) {
		return `{"skill":"csv-clean"}`, nil
	})

	res, err := tune.Score(context.Background(), g, "pdf-extract", "Extracts tables from PDFs.",
		[]suite.TriggerQuery{{Query: "dedupe the rows in this csv", ShouldTrigger: false}},
		tune.ScoreOptions{Distractors: pack, Runs: 2, Threshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 {
		t.Errorf("a silent skill failed a negative query: %+v", res.Results[0])
	}
}

// TestScore_NoneIsAValidAnswer covers the case where the model chooses no
// skill at all.
func TestScore_NoneIsAValidAnswer(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return `{"skill":"none"}`, nil })

	res, err := tune.Score(context.Background(), g, "pdf-extract", "d",
		[]suite.TriggerQuery{{Query: "q", ShouldTrigger: true}},
		tune.ScoreOptions{Distractors: pack, Runs: 1, Threshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 {
		t.Errorf("a positive query passed with no skill selected: %+v", res.Results[0])
	}
}

// TestScore_ThePromptCarriesTheDistractors pins the discrimination half of the
// measurement. Without the pack there is nothing for the skill to lose to, so
// every description scores perfectly on precision.
func TestScore_ThePromptCarriesTheDistractors(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return `{"skill":"none"}`, nil })

	if _, err := tune.Score(context.Background(), g, "pdf-extract", "d",
		[]suite.TriggerQuery{{Query: "q", ShouldTrigger: false}},
		tune.ScoreOptions{Distractors: pack, Runs: 1, Threshold: 0.5}); err != nil {
		t.Fatal(err)
	}

	prompt := g.Calls()[0].Prompt
	for _, want := range []string{"csv-clean", "haiku-writer", "pdf-extract"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not name %q", want)
		}
	}
}
