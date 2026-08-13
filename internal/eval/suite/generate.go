// Package suite is a skill's eval set: the evals/evals.json tasks it is
// measured against, the evals/triggers.json queries its description is
// measured against, and a shipped distractor pack that makes a trigger
// measurement discriminating.
package suite

import (
	"context"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// Permissions for files written into an eval set directory.
const (
	fileMode = 0o644
	dirMode  = 0o755
)

const evalsSchema = `{
  "type": "object",
  "required": ["evals"],
  "properties": {
    "evals": {"type": "array", "items": {"type": "object",
      "required": ["prompt", "expected_output", "expectations"],
      "properties": {
        "prompt":          {"type": "string"},
        "expected_output": {"type": "string"},
        "expectations":    {"type": "array", "items": {"type": "string"}}
      }
    }}
  }
}`

type evalsResult struct {
	Evals []struct {
		Prompt         string   `json:"prompt"`
		ExpectedOutput string   `json:"expected_output"`
		Expectations   []string `json:"expectations"`
	} `json:"evals"`
}

// Generate drafts an eval set of n evals from an approved spec, and derives
// the trigger queries from the spec's own trigger phrases.
//
// One call rather than one per eval: an eval is a prompt and a handful of
// statements, so the whole set fits in one response, and drafting them
// together is what stops n evals from being n rewordings of the same one.
func Generate(ctx context.Context, gw llm.Gateway, sp *spec.SkillSpec, n int) (*EvalSet, []TriggerQuery, error) {
	if gw == nil {
		return nil, nil, fmt.Errorf("suite: Generate requires a gateway")
	}
	if n < 1 {
		return nil, nil, fmt.Errorf("suite: Generate needs at least one eval, got %d", n)
	}

	res, _, err := llm.CompleteJSON[evalsResult](ctx, gw, llm.Req{
		Role:       "suite.evals",
		Prompt:     evalsPrompt(sp, n),
		Schema:     []byte(evalsSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(res.Evals) == 0 {
		return nil, nil, fmt.Errorf("suite: the model drafted no evals for %s", sp.Name)
	}

	set := &EvalSet{SkillName: sp.Name}
	for i, e := range res.Evals {
		if strings.TrimSpace(e.Prompt) == "" {
			continue
		}
		set.Evals = append(set.Evals, Eval{
			ID:             i + 1,
			Prompt:         e.Prompt,
			ExpectedOutput: e.ExpectedOutput,
			Expectations:   e.Expectations,
		})
	}
	if len(set.Evals) == 0 {
		return nil, nil, fmt.Errorf("suite: every drafted eval for %s was empty", sp.Name)
	}
	return set, TriggersFromSpec(sp), nil
}

func evalsPrompt(sp *spec.SkillSpec, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Write %d evaluation tasks for a Claude skill, so its quality can be measured.

The skill is "%s": %s

`, n, sp.Name, sp.Purpose)

	if len(sp.Steps) > 0 {
		b.WriteString("It is supposed to work like this:\n")
		for _, s := range sp.Steps {
			fmt.Fprintf(&b, "- %s (afterwards: %s)\n", s.Action, s.Postcondition)
		}
		b.WriteString("\n")
	}
	if len(sp.Constraints) > 0 {
		// A MUST-NOT is exactly what an expectation expresses, so the
		// constraints become expectations rather than a separate contract.
		b.WriteString("It must respect these rules, and an eval should check the ones it can:\n")
		for _, c := range sp.Constraints {
			fmt.Fprintf(&b, "- %s: %s\n", c.Kind, c.Text)
		}
		b.WriteString("\n")
	}

	b.WriteString(`Each task is one thing a real user would ask for. Write the prompt the way that
user would type it, with concrete detail — file names, column names, a little
context — rather than an abstract instruction.

For each task, write a short description of what success looks like, and then
the expectations: statements that are true of a good answer and false of a bad
one. Make them checkable from the answer itself. An expectation that any
plausible-looking answer satisfies measures nothing, so prefer ones that fail
when the work was not really done.

Cover the ordinary case first, then the variations and the edges. Do not write
a task that needs a file the user has not been given: the agent starts with an
empty workspace.

Reply with JSON only:

{"evals": [{"prompt": "...", "expected_output": "...", "expectations": ["...", "..."]}]}
`)
	return b.String()
}
