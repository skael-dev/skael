package score_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// scriptedGateway answers each call from a function of the prompt, and counts
// calls so the vote protocol is observable.
type scriptedGateway struct {
	calls   atomic.Int32
	prompts []string
	answer  func(n int, prompt string) string
}

func (g *scriptedGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	n := int(g.calls.Add(1))
	g.prompts = append(g.prompts, r.Prompt)
	return llm.Res{Text: g.answer(n, r.Prompt), Model: "fake"}, nil
}

func (g *scriptedGateway) ModelFor(llm.ModelClass) string { return "fake" }

func verdictJSON(winner string, margin float64, quote string) string {
	b, _ := json.Marshal(map[string]any{"winner": winner, "margin": margin, "evidence": []string{quote}})
	return string(b)
}

func demoPair() score.Pair {
	return score.Pair{
		TaskID: "t1", Prompt: "Convert data.csv to a markdown table",
		Skill:    score.Sample{Label: "skill", Transcript: "ran scripts/convert.py and validated the output"},
		Baseline: score.Sample{Label: "baseline", Transcript: "wrote a table by hand with no validation"},
	}
}

func judge(t *testing.T, g llm.Gateway) *score.Judge {
	t.Helper()
	j, err := score.NewJudge(score.JudgeOptions{
		Gateway: g,
		Spec:    &spec.SkillSpec{Name: "demo", Purpose: "convert csv to markdown"},
	})
	if err != nil {
		t.Fatalf("NewJudge: %v", err)
	}
	return j
}

func TestPairwise_SwapsPositionsAndCallsATieWhenTheyDisagree(t *testing.T) {
	// This gateway always prefers whichever candidate was presented first —
	// textbook position bias, and the reason the swap exists at all. A judge
	// without it would score every pair as a skill win.
	g := &scriptedGateway{answer: func(_ int, prompt string) string {
		first := "skill"
		if strings.Index(prompt, "wrote a table by hand") < strings.Index(prompt, "ran scripts/convert.py") {
			first = "baseline"
		}
		quote := "ran scripts/convert.py and validated the output"
		if first == "baseline" {
			quote = "wrote a table by hand with no validation"
		}
		return verdictJSON(first, 0.6, quote)
	}}

	v, err := judge(t, g).Pairwise(context.Background(), demoPair())
	if err != nil {
		t.Fatalf("Pairwise: %v", err)
	}
	if v.Winner != "tie" || !v.Tie {
		t.Errorf("verdict = %+v, want a tie: the two orderings disagreed", v)
	}
	if !v.Swapped {
		t.Error("Swapped = false; the swap is what makes the tie meaningful")
	}
	if g.calls.Load() != 2 {
		t.Errorf("%d calls, want exactly 2 for a decisive disagreement", g.calls.Load())
	}
}

func TestPairwise_AgreementAcrossPositionsIsAWin(t *testing.T) {
	g := &scriptedGateway{answer: func(_ int, _ string) string {
		return verdictJSON("skill", 0.7, "ran scripts/convert.py and validated the output")
	}}
	v, err := judge(t, g).Pairwise(context.Background(), demoPair())
	if err != nil {
		t.Fatal(err)
	}
	if v.Winner != "skill" || v.Tie {
		t.Errorf("verdict = %+v, want a skill win", v)
	}
}

func TestPairwise_ABorderlineMarginBuysAThirdVote(t *testing.T) {
	g := &scriptedGateway{answer: func(n int, _ string) string {
		return verdictJSON("skill", 0.05, "ran scripts/convert.py and validated the output")
	}}
	v, err := judge(t, g).Pairwise(context.Background(), demoPair())
	if err != nil {
		t.Fatal(err)
	}
	// |margin| < 0.15 means the judge is nearly indifferent, which is exactly
	// where a single sample of a nondeterministic process is worth least.
	if g.calls.Load() != 3 {
		t.Errorf("%d calls for a borderline margin, want 3", g.calls.Load())
	}
	if v.Votes != 3 {
		t.Errorf("Votes = %d, want 3", v.Votes)
	}
}

func TestPairwise_ADecisiveMarginDoesNotBuyAThirdVote(t *testing.T) {
	g := &scriptedGateway{answer: func(int, string) string {
		return verdictJSON("skill", 0.8, "ran scripts/convert.py and validated the output")
	}}
	if _, err := judge(t, g).Pairwise(context.Background(), demoPair()); err != nil {
		t.Fatal(err)
	}
	// Sessions are the budget. Spending a third on a decided pair costs a
	// measurement somewhere else.
	if g.calls.Load() != 2 {
		t.Errorf("%d calls for a decisive margin, want 2", g.calls.Load())
	}
}

func TestPairwise_DiscardsAVerdictWithNoEvidence(t *testing.T) {
	g := &scriptedGateway{answer: func(int, string) string {
		return `{"winner":"skill","margin":0.9,"evidence":[]}`
	}}
	_, err := judge(t, g).Pairwise(context.Background(), demoPair())
	if !errors.Is(err, score.ErrNoEvidence) {
		t.Errorf("err = %v, want ErrNoEvidence", err)
	}
}

func TestPairwise_DiscardsAnEvidenceQuoteThatIsNotInTheTranscript(t *testing.T) {
	g := &scriptedGateway{answer: func(int, string) string {
		return verdictJSON("skill", 0.9, "it carefully cross-checked every row against the source")
	}}
	_, err := judge(t, g).Pairwise(context.Background(), demoPair())
	// A quote that does not appear in either transcript means the judge did not
	// read the run. The verdict is not weak evidence, it is no evidence, and
	// accepting it would let a plausible-sounding judgment set 20% of the score.
	if !errors.Is(err, score.ErrNoEvidence) {
		t.Errorf("err = %v, want ErrNoEvidence for a fabricated quote", err)
	}
}

func TestPairwise_RetriesOnceOnAnUnparseableResponse(t *testing.T) {
	g := &scriptedGateway{answer: func(n int, _ string) string {
		if n == 1 {
			return "I cannot comply with that."
		}
		return verdictJSON("skill", 0.7, "ran scripts/convert.py and validated the output")
	}}
	if _, err := judge(t, g).Pairwise(context.Background(), demoPair()); err != nil {
		t.Errorf("Pairwise did not recover from one unparseable response: %v", err)
	}
}

func TestPairwise_ResolvesRealLetterAnswersThroughTheSwapAndTheTie(t *testing.T) {
	// Every other Pairwise test drives the gateway with literal "skill"/
	// "baseline" labels, which never exercises the "A"/"B" resolution branches
	// a real model response takes. This one answers the way the rubric prompt
	// actually asks: "A" or "B", tied to *position*, so it must resolve
	// differently across the swap. Call 1 has skill=A, baseline=B; call 2 (the
	// swap) has baseline=A, skill=B. Answering "A" both times means "whatever
	// is first" both times — i.e. skill, then baseline: a disagreement, and
	// therefore a tie.
	// Evidence differs per call since "A" refers to a different transcript
	// each time; supply the quote that matches whichever candidate is A.
	g := &scriptedGateway{answer: func(_ int, prompt string) string {
		quote := "ran scripts/convert.py and validated the output"
		if strings.Index(prompt, "wrote a table by hand") < strings.Index(prompt, "ran scripts/convert.py") {
			quote = "wrote a table by hand with no validation"
		}
		return verdictJSON("A", 0.6, quote)
	}}

	v, err := judge(t, g).Pairwise(context.Background(), demoPair())
	if err != nil {
		t.Fatalf("Pairwise: %v", err)
	}
	if v.Winner != "tie" || !v.Tie || !v.Swapped {
		t.Errorf("verdict = %+v, want a tie from the swap: \"A\" resolves to skill in call 1 and baseline in call 2", v)
	}

	// Now answer "B" consistently: call 1 (skill=A, baseline=B) resolves to
	// baseline; call 2 (baseline=A, skill=B) resolves to skill. Also a
	// disagreement — the letter, not the label, is what must be tracked.
	g2 := &scriptedGateway{answer: func(_ int, prompt string) string {
		quote := "wrote a table by hand with no validation"
		if strings.Index(prompt, "wrote a table by hand") < strings.Index(prompt, "ran scripts/convert.py") {
			quote = "ran scripts/convert.py and validated the output"
		}
		return verdictJSON("B", 0.6, quote)
	}}
	v2, err := judge(t, g2).Pairwise(context.Background(), demoPair())
	if err != nil {
		t.Fatalf("Pairwise: %v", err)
	}
	if v2.Winner != "tie" || !v2.Tie || !v2.Swapped {
		t.Errorf("verdict = %+v, want a tie: \"B\" also resolves to a different side each call", v2)
	}

	// Finally, "A" in call 1 (-> skill) and "B" in call 2 (-> skill) agree:
	// both calls genuinely pick skill once the letters are resolved against
	// their own ordering, so this must be a clean win, not a tie.
	g3 := &scriptedGateway{answer: func(n int, _ string) string {
		if n == 1 {
			return verdictJSON("A", 0.6, "ran scripts/convert.py and validated the output")
		}
		return verdictJSON("B", 0.6, "ran scripts/convert.py and validated the output")
	}}
	v3, err := judge(t, g3).Pairwise(context.Background(), demoPair())
	if err != nil {
		t.Fatalf("Pairwise: %v", err)
	}
	if v3.Winner != "skill" || v3.Tie {
		t.Errorf("verdict = %+v, want a skill win: A->skill and B->skill agree once resolved", v3)
	}
}

func TestJudgeOptions_MaxVotesCapsTheThirdVote(t *testing.T) {
	g := &scriptedGateway{answer: func(int, string) string {
		// A borderline margin that would normally buy a third call.
		return verdictJSON("skill", 0.05, "ran scripts/convert.py and validated the output")
	}}
	j, err := score.NewJudge(score.JudgeOptions{
		Gateway:  g,
		Spec:     &spec.SkillSpec{Name: "demo", Purpose: "convert csv to markdown"},
		MaxVotes: 2,
	})
	if err != nil {
		t.Fatalf("NewJudge: %v", err)
	}
	v, err := j.Pairwise(context.Background(), demoPair())
	if err != nil {
		t.Fatal(err)
	}
	if g.calls.Load() != 2 {
		t.Errorf("%d calls, want 2: MaxVotes:2 must disable the borderline third call", g.calls.Load())
	}
	if v.Votes != 2 {
		t.Errorf("Votes = %d, want 2", v.Votes)
	}
}

func TestSemantic_ScoresARuleAndCitesIt(t *testing.T) {
	g := &scriptedGateway{answer: func(int, string) string {
		return `{"satisfied":true,"confidence":1,"evidence":["wrote a table by hand with no validation"]}`
	}}
	got, quotes, err := judge(t, g).Semantic(context.Background(),
		contract.SemanticRule{ID: "r1", Text: "the report's tone stays formal"},
		score.Sample{Label: "skill", Transcript: "wrote a table by hand with no validation"})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("score = %v, want 1", got)
	}
	if len(quotes) == 0 {
		t.Error("a semantic verdict with no quote is unreviewable")
	}
}

func TestSemantic_ScoresZeroWhenSatisfiedButUncited(t *testing.T) {
	g := &scriptedGateway{answer: func(int, string) string {
		// "satisfied" with no evidence at all.
		return `{"satisfied":true,"confidence":1,"evidence":[]}`
	}}
	got, _, err := judge(t, g).Semantic(context.Background(),
		contract.SemanticRule{ID: "r1", Text: "the report's tone stays formal"},
		score.Sample{Label: "skill", Transcript: "wrote a table by hand with no validation"})
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("score = %v, want 0: an unsupported \"yes\" is not evidence of adherence", got)
	}

	g2 := &scriptedGateway{answer: func(int, string) string {
		// "satisfied" with a quote that is not in the transcript.
		return `{"satisfied":true,"confidence":1,"evidence":["a sentence that never appears anywhere"]}`
	}}
	got2, _, err := judge(t, g2).Semantic(context.Background(),
		contract.SemanticRule{ID: "r1", Text: "the report's tone stays formal"},
		score.Sample{Label: "skill", Transcript: "wrote a table by hand with no validation"})
	if err != nil {
		t.Fatal(err)
	}
	if got2 != 0 {
		t.Errorf("score = %v, want 0: a fabricated quote cannot support a satisfied verdict", got2)
	}
}

func TestNewJudge_RequiresAGatewayAndASpec(t *testing.T) {
	if _, err := score.NewJudge(score.JudgeOptions{}); err == nil {
		t.Error("NewJudge accepted no gateway")
	}
	if _, err := score.NewJudge(score.JudgeOptions{Gateway: &scriptedGateway{}}); err == nil {
		t.Error("NewJudge accepted no spec; the rubric is derived from it")
	}
}

func TestPairwise_TheRubricIsAnchoredAndMentionsTheSkillsPurpose(t *testing.T) {
	g := &scriptedGateway{answer: func(int, string) string {
		return verdictJSON("skill", 0.7, "ran scripts/convert.py and validated the output")
	}}
	if _, err := judge(t, g).Pairwise(context.Background(), demoPair()); err != nil {
		t.Fatal(err)
	}
	p := g.prompts[0]
	// An unanchored "which is better" prompt is where judge nondeterminism
	// comes from. The anchors, the purpose, and the evidence requirement are
	// the mitigations, so their presence is asserted rather than assumed.
	for _, want := range []string{"convert csv to markdown", "1", "5", "evidence", "verbatim"} {
		if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
			t.Errorf("rubric prompt missing %q:\n%s", want, p)
		}
	}
	if !strings.Contains(p, fmt.Sprintf("Task: %s", demoPair().Prompt)) {
		t.Error("rubric prompt does not state the task")
	}
}
