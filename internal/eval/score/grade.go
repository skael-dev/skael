package score

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// Expectation is one graded statement from an eval's expectations list.
type Expectation struct {
	Text     string `json:"text"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

// Grade is one agent session's expectations, graded.
type Grade struct {
	Expectations []Expectation `json:"expectations"`
	Passed       int           `json:"passed"`
	Total        int           `json:"total"`
}

// Rate is the fraction of this session's expectations that passed.
func (g Grade) Rate() float64 {
	if g.Total == 0 {
		return 0
	}
	return float64(g.Passed) / float64(g.Total)
}

// Run is one agent session, as the grader sees it.
type Run struct {
	Prompt         string
	ExpectedOutput string
	Transcript     string
	// Outputs is a rendering of the files the session produced. The grader is
	// told to fail an expectation it can only confirm from the agent's own
	// account, so a session with no outputs rendered here grades harder.
	Outputs string
}

// Grader marks an eval's expectations against one session.
type Grader struct{ gateway llm.Gateway }

// NewGrader returns a Grader backed by g.
func NewGrader(g llm.Gateway) (*Grader, error) {
	if g == nil {
		return nil, errors.New("score.NewGrader: gateway is required")
	}
	return &Grader{gateway: g}, nil
}

type gradeResponse struct {
	Expectations []struct {
		Passed   bool   `json:"passed"`
		Evidence string `json:"evidence"`
	} `json:"expectations"`
}

// Grade marks every expectation against r in one call.
//
// One call rather than one per expectation: the evidence for each verdict is
// the same transcript, and a per-expectation call multiplies the cost of a
// score by the length of its longest expectations list.
func (g *Grader) Grade(ctx context.Context, expectations []string, r Run) (Grade, error) {
	if len(expectations) == 0 {
		return Grade{}, errors.New("score.Grade: no expectations to grade")
	}

	resp, err := llm.CompleteJSON[gradeResponse](ctx, g.gateway, llm.Req{
		Role:       "score.grade",
		Prompt:     gradePrompt(expectations, r),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return Grade{}, err
	}
	if len(resp.Expectations) != len(expectations) {
		return Grade{}, fmt.Errorf("score.Grade: graded %d expectations, asked for %d",
			len(resp.Expectations), len(expectations))
	}

	out := Grade{Expectations: make([]Expectation, len(expectations)), Total: len(expectations)}
	for i, want := range expectations {
		v := resp.Expectations[i]
		// Text comes from the eval, never from the response. A model that
		// reworded an expectation into one it could satisfy would otherwise
		// have its rewording stored as the thing that was checked.
		e := Expectation{Text: want, Passed: v.Passed, Evidence: strings.TrimSpace(v.Evidence)}
		if e.Passed && e.Evidence == "" {
			// The burden of proof to pass sits with the expectation. A pass
			// with nothing cited has not discharged it, and an uncited pass is
			// the shape a hallucinated one takes.
			e.Passed = false
			e.Evidence = "no evidence cited for a pass"
		}
		if e.Passed {
			out.Passed++
		}
		out.Expectations[i] = e
	}
	return out, nil
}

// EvalRate is one eval's score across its runs: the mean of the per-run pass
// rates.
//
// Mean rather than median. At two or three runs a median cannot tell "passed
// twice of three" from "passed once of three", which is the flakiness the
// repeat runs exist to find.
func EvalRate(gs []Grade) (float64, error) {
	if len(gs) == 0 {
		return 0, errors.New("score.EvalRate: no graded runs")
	}
	var sum float64
	for _, g := range gs {
		if g.Total == 0 {
			return 0, errors.New("score.EvalRate: a graded run has no expectations")
		}
		sum += g.Rate()
	}
	return sum / float64(len(gs)), nil
}

// MemberScore is one panel member's 0–100 score: the mean of its evals' rates.
//
// The mean over evals rather than over pooled expectations. Pooling weighs an
// eval by how many expectations it happens to carry, so five easy statements
// added to an easy eval would raise the score without improving the skill.
func MemberScore(evalRates []float64) (float64, error) {
	if len(evalRates) == 0 {
		return 0, errors.New("score.MemberScore: no scored evals")
	}
	var sum float64
	for _, r := range evalRates {
		if r < 0 || r > 1 {
			return 0, fmt.Errorf("score.MemberScore: %v is not a rate in [0,1]", r)
		}
		sum += r
	}
	return 100 * sum / float64(len(evalRates)), nil
}

// gradePrompt renders the grader instruction. It is adapted from Anthropic's
// skills/skill-creator agents/grader.md, Apache 2.0 — see NOTICE.
func gradePrompt(expectations []string, r Run) string {
	var b strings.Builder
	b.WriteString(`You are grading one agent session against a list of expectations.

For each expectation, decide whether it is true of this session, and cite the
evidence you decided from.

Pass an expectation only when the transcript or the outputs show it is true and
the evidence reflects the task genuinely being done. Fail it when you find no
evidence, when the evidence contradicts it, when you cannot verify it from what
you were given, or when it is satisfied only on the surface — a file with the
right name and the wrong contents fails.

The burden of proof to pass sits with the expectation. There is no partial
credit: each one passes or fails.

`)
	b.WriteString("## The task the agent was given\n\n")
	b.WriteString(r.Prompt)
	if r.ExpectedOutput != "" {
		b.WriteString("\n\n## What success looks like\n\n")
		b.WriteString(r.ExpectedOutput)
	}
	b.WriteString("\n\n## Transcript\n\n")
	b.WriteString(r.Transcript)
	b.WriteString("\n\n## Files the session produced\n\n")
	if r.Outputs == "" {
		b.WriteString("(none were captured)")
	} else {
		b.WriteString(r.Outputs)
	}

	b.WriteString("\n\n## Expectations\n\n")
	for i, e := range expectations {
		fmt.Fprintf(&b, "%d. %s\n", i+1, e)
	}

	fmt.Fprintf(&b, `
Reply with JSON only, one entry per expectation, in the order above:

{"expectations": [{"passed": true, "evidence": "quote or describe what you found"}]}

Return exactly %d entries.
`, len(expectations))
	return b.String()
}
