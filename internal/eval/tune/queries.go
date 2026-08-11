package tune

import (
	"context"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/suite"
)

const queriesSchema = `{
  "type": "object",
  "required": ["queries"],
  "properties": {
    "queries": {"type": "array", "items": {"type": "object",
      "required": ["query", "should_trigger"],
      "properties": {
        "query":          {"type": "string"},
        "should_trigger": {"type": "boolean"}
      }
    }}
  }
}`

type queriesResult struct {
	Queries []suite.TriggerQuery `json:"queries"`
}

// TopUp extends have with generated queries until it holds want of them.
// TopUp returns an already long enough set unchanged. It makes no model call
// in that case.
//
// The set is the tuner's whole measurement, so a short one produces a score
// with no resolution. The eval tiers also read the set. That is why the
// caller writes the enlarged set back to evals/triggers.json.
func TopUp(ctx context.Context, g llm.Gateway, skillName, description, skillBody string,
	have []suite.TriggerQuery, want int) ([]suite.TriggerQuery, error) {

	if len(have) >= want {
		return have, nil
	}
	if g == nil {
		return nil, fmt.Errorf("tune: TopUp requires a gateway")
	}

	var positive, negative int
	for _, q := range have {
		if q.ShouldTrigger {
			positive++
		} else {
			negative++
		}
	}
	needPositive := want/2 - positive
	needNegative := want - want/2 - negative
	if needPositive < 0 {
		needPositive = 0
	}
	if needNegative < 0 {
		needNegative = 0
	}

	res, err := llm.CompleteJSON[queriesResult](ctx, g, llm.Req{
		Role:       "tune.queries",
		Prompt:     queriesPrompt(skillName, description, skillBody, have, needPositive, needNegative),
		Schema:     []byte(queriesSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(have))
	out := append([]suite.TriggerQuery(nil), have...)
	for _, q := range have {
		seen[strings.ToLower(strings.TrimSpace(q.Query))] = true
	}
	for _, q := range res.Queries {
		key := strings.ToLower(strings.TrimSpace(q.Query))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, q)
		if len(out) == want {
			break
		}
	}
	if len(out) < want {
		return nil, fmt.Errorf("tune: asked for %d trigger queries for %s and got %d", want, skillName, len(out))
	}
	return out, nil
}

func queriesPrompt(skillName, description, skillBody string, have []suite.TriggerQuery, needPositive, needNegative int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Write trigger evaluation queries for an agent skill called %q.

The skill's description is:

%q

What it does:

%s

Write %d queries that MUST fire this skill, and %d that must NOT.

`, skillName, description, skillBody, needPositive, needNegative)

	b.WriteString(`Every query is something a real person would type. Write it with concrete
detail: file names, column names, a company, a little backstory. Vary the
length. Some are lower case, some carry a typo or an abbreviation. Prefer edge
cases over clear-cut ones.

Bad: "Format this data". "Extract text from PDF".

Good: "ok so my boss just sent me this xlsx file (its in my downloads, called
something like 'Q4 sales final FINAL v2.xlsx') and she wants me to add a column
that shows the profit margin as a percentage. revenue is in column C and costs
are in column D i think"

For the queries that must fire, cover different phrasings of one intent, some
formal and some casual. Include a case where the person names neither the skill
nor the file type but plainly needs it.

For the queries that must not fire, write near-misses: adjacent domains, and
phrasing where a naive keyword match would fire but should not. An obviously
irrelevant query tests nothing.

`)

	if len(have) > 0 {
		b.WriteString("The set already holds these. Do not repeat one:\n")
		for _, q := range have {
			fmt.Fprintf(&b, "  - %q (fires: %v)\n", q.Query, q.ShouldTrigger)
		}
		b.WriteString("\n")
	}

	b.WriteString(`Reply with JSON only:

{"queries": [{"query": "...", "should_trigger": true}]}
`)
	return b.String()
}
