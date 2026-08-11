package tune

import (
	"context"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

const improveSchema = `{
  "type": "object",
  "required": ["description"],
  "properties": {"description": {"type": "string"}}
}`

type improveResult struct {
	Description string `json:"description"`
}

// Attempt is one iteration's description and what it scored.
type Attempt struct {
	Iteration    int         `json:"iteration"`
	Description  string      `json:"description"`
	Train        ScoreResult `json:"train"`
	Test         ScoreResult `json:"test"`
	TestMeasured bool        `json:"test_measured"`
}

// Improve asks for a new description, given what the current one failed on.
//
// The history it sends is blinded to every held-out score. A model that sees
// the test score tunes against it. The held-out half then no longer stays
// held out. This is the one thing the split exists to prevent.
func Improve(ctx context.Context, g llm.Gateway, skillName, skillBody, current string,
	train ScoreResult, history []Attempt) (string, error) {

	if g == nil {
		return "", fmt.Errorf("tune: Improve requires a gateway")
	}

	prompt := improvePrompt(skillName, skillBody, current, train, history)
	res, err := llm.CompleteJSON[improveResult](ctx, g, llm.Req{
		Role: "tune.improve", Prompt: prompt, Schema: []byte(improveSchema), ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.Description)
	if out == "" {
		return "", fmt.Errorf("tune: the model proposed an empty description for %s", skillName)
	}
	if len(out) <= spec.MaxDescription {
		return out, nil
	}

	// The prompt already states the limit. A model that went past it anyway
	// gets one rewrite that quotes its own answer back. The Python does this
	// across two turns. This does it in one prompt.
	shorten := fmt.Sprintf(`%s

A previous attempt produced this description, which at %d characters is over
the %d character hard limit:

%q

Rewrite it under the limit. Keep the trigger words and the intent coverage
that matter most.`, prompt, len(out), spec.MaxDescription, out)

	res, err = llm.CompleteJSON[improveResult](ctx, g, llm.Req{
		Role: "tune.shorten", Prompt: shorten, Schema: []byte(improveSchema), ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return "", err
	}
	short := strings.TrimSpace(res.Description)
	if short == "" {
		return "", fmt.Errorf("tune: the model proposed an empty shortened description for %s", skillName)
	}
	return short, nil
}

func improvePrompt(skillName, skillBody, current string, train ScoreResult, history []Attempt) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You are improving the description of an agent skill called %q.

The description appears in the agent's list of available skills. When a user
sends a query, the agent decides whether to consult the skill from the name and
this description alone. A good description fires on relevant queries and stays
silent on irrelevant ones.

The current description is:

%q

It scored %d/%d.

`, skillName, current, train.Passed, train.Total)

	var missed, wrong []QueryResult
	for _, r := range train.Results {
		switch {
		case r.ShouldTrigger && !r.Pass:
			missed = append(missed, r)
		case !r.ShouldTrigger && !r.Pass:
			wrong = append(wrong, r)
		}
	}
	if len(missed) > 0 {
		b.WriteString("It failed to fire on these, and it should have:\n")
		for _, r := range missed {
			fmt.Fprintf(&b, "  - %q (fired %d of %d times)\n", r.Query, r.Triggers, r.Runs)
		}
		b.WriteString("\n")
	}
	if len(wrong) > 0 {
		b.WriteString("It fired on these, and it should not have:\n")
		for _, r := range wrong {
			fmt.Fprintf(&b, "  - %q (fired %d of %d times)\n", r.Query, r.Triggers, r.Runs)
		}
		b.WriteString("\n")
	}

	if len(history) > 0 {
		b.WriteString("Earlier attempts. Do not repeat one. Try something structurally different:\n\n")
		for _, h := range history {
			fmt.Fprintf(&b, "  scored %d/%d: %q\n", h.Train.Passed, h.Train.Total, h.Description)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, `What the skill does, for context:

%s

Write a better description. Generalize from the failures to broader categories
of user intent. Do not write a growing list of the specific queries above: that
overfits, and the description is injected into every query, so its length is a
cost paid everywhere.

Stay under about 200 words. The hard limit is %d characters.

What works well:
  - Write it in the imperative. "Use this skill for", not "this skill does".
  - Describe what the user wants to achieve, not how the skill works.
  - Make it distinctive. It competes with other skills for attention.
  - Change the structure when repeated attempts keep failing.

Reply with JSON only: {"description": "..."}.
`, skillBody, spec.MaxDescription)
	return b.String()
}
