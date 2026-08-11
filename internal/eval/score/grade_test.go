package score_test

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/score"
)

func grader(t *testing.T, g llm.Gateway) *score.Grader {
	t.Helper()
	gr, err := score.NewGrader(g)
	if err != nil {
		t.Fatalf("NewGrader: %v", err)
	}
	return gr
}

func TestGrade_MarksEachExpectation(t *testing.T) {
	gw := fake.New(`{"expectations":[
	  {"passed":true,"evidence":"Step 3 wrote out/tables.csv"},
	  {"passed":false,"evidence":"no header row in the output"}]}`)

	got, err := grader(t, gw).Grade(context.Background(),
		[]string{"out/tables.csv exists", "the csv has a header row"},
		score.Run{Prompt: "extract the tables", Transcript: "…"})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if got.Total != 2 || got.Passed != 1 {
		t.Fatalf("passed=%d total=%d, want 1 and 2", got.Passed, got.Total)
	}
	if got.Rate() != 0.5 {
		t.Errorf("Rate() = %v, want 0.5", got.Rate())
	}
}

// A model that rewords an expectation into one it can satisfy must not have
// its rewording stored as the thing that was checked.
func TestGrade_KeepsTheEvalsOwnExpectationText(t *testing.T) {
	gw := fake.New(`{"expectations":[{"text":"the agent tried its best","passed":true,"evidence":"e"}]}`)

	got, err := grader(t, gw).Grade(context.Background(),
		[]string{"out/tables.csv has 12 rows"}, score.Run{Prompt: "p"})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if got.Expectations[0].Text != "out/tables.csv has 12 rows" {
		t.Errorf("text = %q, want the eval's own wording", got.Expectations[0].Text)
	}
}

// The burden of proof to pass sits with the expectation, so an uncited pass is
// not one.
func TestGrade_DowngradesAPassWithNoEvidence(t *testing.T) {
	gw := fake.New(`{"expectations":[{"passed":true,"evidence":"   "}]}`)

	got, err := grader(t, gw).Grade(context.Background(),
		[]string{"the output names John Smith"}, score.Run{Prompt: "p"})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if got.Expectations[0].Passed {
		t.Error("an uncited pass survived")
	}
	if got.Passed != 0 {
		t.Errorf("Passed = %d, want 0", got.Passed)
	}
	if got.Expectations[0].Evidence == "" {
		t.Error("the downgrade recorded no reason")
	}
}

// A short reply would otherwise silently drop expectations from the
// denominator, which raises the score.
func TestGrade_RefusesAWrongVerdictCount(t *testing.T) {
	gw := fake.New(
		`{"expectations":[{"passed":true,"evidence":"e"}]}`,
		`{"expectations":[{"passed":true,"evidence":"e"}]}`)

	_, err := grader(t, gw).Grade(context.Background(),
		[]string{"a", "b"}, score.Run{Prompt: "p"})
	if err == nil {
		t.Fatal("Grade accepted one verdict for two expectations")
	}
	if !strings.Contains(err.Error(), "asked for 2") {
		t.Errorf("err = %v, want it to name the counts", err)
	}
}

func TestGrade_RefusesAnEmptyExpectationList(t *testing.T) {
	if _, err := grader(t, fake.New()).Grade(context.Background(), nil, score.Run{}); err == nil {
		t.Error("Grade accepted an eval with no expectations")
	}
}

func TestEvalRate_IsTheMeanAcrossRuns(t *testing.T) {
	// Two of three runs passed the single expectation. A median would report
	// this as 1.0 and lose the flakiness the repeats exist to find.
	gs := []score.Grade{
		{Passed: 1, Total: 1}, {Passed: 1, Total: 1}, {Passed: 0, Total: 1},
	}
	got, err := score.EvalRate(gs)
	if err != nil {
		t.Fatalf("EvalRate: %v", err)
	}
	if want := 2.0 / 3.0; got != want {
		t.Errorf("EvalRate = %v, want %v", got, want)
	}
}

func TestEvalRate_RefusesNoRuns(t *testing.T) {
	if _, err := score.EvalRate(nil); err == nil {
		t.Error("EvalRate accepted zero runs")
	}
}

// Weighing evals by expectation count would let five easy statements added to
// an easy eval raise the score without improving the skill.
func TestMemberScore_WeighsEveryEvalEqually(t *testing.T) {
	got, err := score.MemberScore([]float64{1, 0})
	if err != nil {
		t.Fatalf("MemberScore: %v", err)
	}
	if got != 50 {
		t.Errorf("MemberScore = %v, want 50", got)
	}
}

func TestMemberScore_RefusesNoEvalsAndBadRates(t *testing.T) {
	if _, err := score.MemberScore(nil); err == nil {
		t.Error("MemberScore accepted zero evals")
	}
	if _, err := score.MemberScore([]float64{1.5}); err == nil {
		t.Error("MemberScore accepted a rate above 1")
	}
}
