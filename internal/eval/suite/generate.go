package suite

import (
	"context"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// suiteSchema constrains the model's output. It is a trimmed JSON Schema
// rather than the full type: the gateway includes it in the prompt, and
// every byte of schema competes with the actual instructions for attention.
const suiteSchema = `{
  "type": "object",
  "required": ["tasks", "triggers"],
  "properties": {
    "tasks": {"type": "array", "items": {"type": "object",
      "properties": {
        "id":        {"type": "string"},
        "kind":      {"enum": ["happy", "variant", "edge", "negative-trigger"]},
        "prompt_md": {"type": "string"},
        "env_frag":  {"type": "string"},
        "oracle":    {"type": "string"},
        "verifier":  {"type": "string"}
      },
      "required": ["id", "kind", "prompt_md", "oracle", "verifier"]
    }},
    "triggers": {"type": "object",
      "properties": {
        "positive": {"type": "array", "items": {"type": "string"}},
        "negative": {"type": "array", "items": {"type": "string"}}
      },
      "required": ["positive", "negative"]
    }
  }
}`

// generateResult is the raw shape of the model's suite.draft response.
type generateResult struct {
	Tasks []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		PromptMD string `json:"prompt_md"`
		EnvFrag  string `json:"env_frag,omitempty"`
		Oracle   string `json:"oracle"`
		Verifier string `json:"verifier"`
	} `json:"tasks"`
	Triggers struct {
		Positive []string `json:"positive"`
		Negative []string `json:"negative"`
	} `json:"triggers"`
}

// Generate drafts an evaluation suite for s in a single gateway call: roughly
// ten core task packages — a happy path, paraphrase variants, and edge
// cases — each shipping its own oracle and verifier, plus a trigger set of
// positive and hard-negative example prompts.
//
// Every task must ship an oracle: it is the reference solution the task's
// own verifier must pass. Without one a broken task is indistinguishable
// from a broken skill, so this is asked for explicitly rather than left
// optional. Splitting into dev/holdout and writing to disk are separate
// steps (Split, Write) so a caller can inspect the draft first.
func Generate(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (*Suite, error) {
	res, err := llm.CompleteJSON[generateResult](ctx, g, llm.Req{
		Role:       "suite.draft",
		Prompt:     draftPrompt(s),
		Schema:     []byte(suiteSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, fmt.Errorf("suite: drafting suite: %w", err)
	}

	tasks := make([]TaskPkg, 0, len(res.Tasks))
	for _, t := range res.Tasks {
		tasks = append(tasks, TaskPkg{
			ID:       t.ID,
			Kind:     t.Kind,
			PromptMD: t.PromptMD,
			EnvFrag:  t.EnvFrag,
			Oracle:   t.Oracle,
			Verifier: t.Verifier,
		})
	}

	return &Suite{
		Tasks: tasks,
		Triggers: TriggerSet{
			Positive: res.Triggers.Positive,
			Negative: res.Triggers.Negative,
		},
	}, nil
}

// draftPrompt asks for the suite in the D12/SkillsBench task layout, stating
// explicitly that every task needs an oracle and a verifier that can fail,
// and that trigger negatives must be adjacent-domain near-misses rather than
// obviously irrelevant prompts.
func draftPrompt(s *spec.SkillSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Draft an evaluation suite for the skill %q.\n\n", s.Name)
	fmt.Fprintf(&b, "Purpose: %s\n", s.Purpose)
	fmt.Fprintf(&b, "Description: %s\n\n", s.Description)

	b.WriteString("Example prompts that should trigger this skill:\n")
	for _, t := range s.Triggers {
		if !t.Negative {
			fmt.Fprintf(&b, "- %s\n", t.Text)
		}
	}

	b.WriteString("\nSteps the skill is meant to carry out:\n")
	for _, st := range s.Steps {
		fmt.Fprintf(&b, "- [%s] %s (postcondition: %s)\n", st.ID, st.Action, st.Postcondition)
	}

	if len(s.Constraints) > 0 {
		b.WriteString("\nConstraints the skill's behavior must respect:\n")
		for _, c := range s.Constraints {
			fmt.Fprintf(&b, "- (%s, %s) %s\n", c.Kind, c.Severity, c.Text)
		}
	}

	b.WriteString("\nProduce about 10 task packages: a happy-path task, paraphrase variants, " +
		"and edge cases. Each task must carry:\n")
	b.WriteString("- \"id\": a short, unique, filesystem-safe slug — it becomes a directory name.\n")
	b.WriteString("- \"kind\": one of \"happy\", \"variant\", \"edge\", \"negative-trigger\".\n")
	b.WriteString("- \"prompt_md\": the task prompt given to the agent under test.\n")
	b.WriteString("- \"oracle\": a shell script (written to oracle/solve.sh) that is a reference " +
		"solution to the task. It must pass the task's own verifier — if it doesn't, the task " +
		"itself is void rather than the skill under test being blamed, so every task needs one.\n")
	b.WriteString("- \"verifier\": a shell script (written to verifier/test.sh) that exits zero " +
		"only when the task was solved correctly and exits non-zero on any failure. A verifier " +
		"that cannot fail cannot detect a broken skill.\n\n")

	b.WriteString("Also produce a trigger set: 8 positive prompts that should activate this " +
		"skill, and 8 hard negatives. A hard negative is an adjacent-domain near-miss — it must " +
		"read as though it could plausibly belong to this skill's domain, without actually " +
		"matching its purpose. An obviously irrelevant negative tests nothing.\n\n")

	b.WriteString("Respond with JSON: {\"tasks\": [...], \"triggers\": {\"positive\": [...], " +
		"\"negative\": [...]}}. No prose outside the JSON.\n")
	return b.String()
}
