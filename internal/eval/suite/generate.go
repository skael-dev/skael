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
        "setup":     {"type": "string"},
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
		Setup    string `json:"setup,omitempty"`
		Oracle   string `json:"oracle"`
		Verifier string `json:"verifier"`
	} `json:"tasks"`
	Triggers struct {
		Positive []string `json:"positive"`
		Negative []string `json:"negative"`
	} `json:"triggers"`
}

// defaultTaskCount is the authored path's suite size.
const defaultTaskCount = 10

// Generate drafts a suite of the default size. See GenerateN.
func Generate(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (*Suite, error) {
	return GenerateN(ctx, g, s, defaultTaskCount)
}

// GenerateN drafts an evaluation suite for s in a single gateway call: n core
// task packages — a happy path, paraphrase variants, and edge cases — each
// shipping its own oracle and verifier, plus a trigger set of positive and
// hard-negative example prompts. The derived-suite path asks for more than
// the authored default because it has no author to fix a task the oracle
// gate voids — the extra tasks are the headroom that lets a machine-generated
// suite still satisfy runner.BuildPlan after voids.
//
// Every task must ship an oracle: it is the reference solution the task's
// own verifier must pass. Without one a broken task is indistinguishable
// from a broken skill, so this is asked for explicitly rather than left
// optional. Splitting into dev/holdout and writing to disk are separate
// steps (Split, Write) so a caller can inspect the draft first.
func GenerateN(ctx context.Context, g llm.Gateway, s *spec.SkillSpec, n int) (*Suite, error) {
	res, err := llm.CompleteJSON[generateResult](ctx, g, llm.Req{
		Role:       "suite.draft",
		Prompt:     draftPrompt(s, n),
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
			Setup:    t.Setup,
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
func draftPrompt(s *spec.SkillSpec, n int) string {
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

	fmt.Fprintf(&b, "\nProduce about %d task packages: a happy-path task, paraphrase variants, "+
		"and edge cases. Each task must carry:\n", n)
	b.WriteString("- \"id\": a short, unique, filesystem-safe slug — it becomes a directory name.\n")
	b.WriteString("- \"kind\": one of \"happy\", \"variant\", \"edge\", \"negative-trigger\".\n")
	b.WriteString("- \"prompt_md\": the task prompt given to the agent under test.\n")
	b.WriteString("- \"setup\": optional. A shell script (written to environment/setup.sh) that " +
		"creates the task's input files, run in the workspace before the agent and before the " +
		"oracle. If the prompt refers to a file, this is the only thing that creates it — the " +
		"oracle and the verifier must read those inputs, never write them. Omit it for a task " +
		"that needs no input files. Use only ordinary shell (mkdir, heredocs, printf); it is " +
		"not a Dockerfile, and a package the task needs belongs in the skill spec's deps.\n")
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
