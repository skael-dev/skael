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

const topUpReply = `{"queries":[
  {"query":"my boss sent me a quarterly report pdf and i need the tables in a spreadsheet","should_trigger":true},
  {"query":"dedupe the rows in sales_2026.csv by email column","should_trigger":false}
]}`

// TestTopUp_AddsOnlyWhatIsMissing pins that an adequate set costs nothing. A
// call made every run spends a model call to learn there is no work.
func TestTopUp_AddsOnlyWhatIsMissing(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return topUpReply, nil })
	have := set(8, 8)

	got, err := tune.TopUp(context.Background(), g, "pdf-extract", "d", "body", have, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 16 {
		t.Errorf("TopUp returned %d queries, want the 16 it was given", len(got))
	}
	if len(g.Calls()) != 0 {
		t.Errorf("made %d calls for a set that was already long enough", len(g.Calls()))
	}
}

func TestTopUp_KeepsTheQueriesItWasGiven(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return topUpReply, nil })
	have := []suite.TriggerQuery{{Query: "keep me", ShouldTrigger: true}}

	got, err := tune.TopUp(context.Background(), g, "pdf-extract", "d", "body", have, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Query != "keep me" {
		t.Errorf("the first query is %q, want the one that was already there", got[0].Query)
	}
	if len(got) != 3 {
		t.Errorf("TopUp returned %d queries, want 3", len(got))
	}
}

// TestTopUp_ThePromptShowsWhatExists ensures the model does not write a query
// the set already holds.
func TestTopUp_ThePromptShowsWhatExists(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return topUpReply, nil })

	if _, err := tune.TopUp(context.Background(), g, "pdf-extract", "d", "body",
		[]suite.TriggerQuery{{Query: "already here", ShouldTrigger: true}}, 3); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Calls()[0].Prompt, "already here") {
		t.Error("the prompt does not show the queries the set already holds")
	}
}
