package suite

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// outlineSchema constrains the outline call. Stubs only: the whole point of
// the split is that this response cannot grow with the size of the scripts.
const outlineSchema = `{
  "type": "object",
  "required": ["tasks", "triggers"],
  "properties": {
    "tasks": {"type": "array", "items": {"type": "object",
      "properties": {
        "id":     {"type": "string"},
        "kind":   {"enum": ["happy", "variant", "edge", "negative-trigger"]},
        "intent": {"type": "string"}
      },
      "required": ["id", "kind", "intent"]
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

// expandSchema constrains one task package.
const expandSchema = `{
  "type": "object",
  "required": ["prompt_md", "oracle", "verifier"],
  "properties": {
    "prompt_md": {"type": "string"},
    "setup":     {"type": "string"},
    "oracle":    {"type": "string"},
    "verifier":  {"type": "string"}
  }
}`

// outlineResult is the raw shape of the suite.outline response.
type outlineResult struct {
	Tasks []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Intent string `json:"intent"`
	} `json:"tasks"`
	Triggers struct {
		Positive []string `json:"positive"`
		Negative []string `json:"negative"`
	} `json:"triggers"`
}

// expandResult is the raw shape of one suite.expand response.
type expandResult struct {
	PromptMD string `json:"prompt_md"`
	Setup    string `json:"setup,omitempty"`
	Oracle   string `json:"oracle"`
	Verifier string `json:"verifier"`
}

const (
	roleOutline = "suite.outline"
	roleExpand  = "suite.expand"
)

// expandConcurrency bounds the fan-out. Wide enough to be worth doing, narrow
// enough not to manufacture the 429s the gateway would then have to retry.
const expandConcurrency = 4

// Dropped is a stub that could not be expanded into a task package. It is
// returned rather than logged because a thin suite's reason is the first thing
// anyone asks about, and derive turns these into void checks.
type Dropped struct {
	TaskID string
	Kind   string
	Reason string
}

// defaultTaskCount is the authored path's suite size.
const defaultTaskCount = 10

// Generate drafts an evaluation suite for s at the authored path's size.
func Generate(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (*Suite, []Dropped, error) {
	return GenerateN(ctx, g, s, defaultTaskCount)
}

// GenerateN drafts an evaluation suite for s in two phases: one outline call
// returning n task stubs and the trigger set, then one call per stub to write
// that task's prompt, setup, oracle and verifier.
//
// The split exists because a single call's output grew with n while the
// gateway's cap did not, so a verbose skill truncated and lost the whole
// draft. The outline is bounded by construction; an expansion covers one task.
// A failed expansion drops that task and returns it in Dropped — runner.BuildPlan
// is what decides whether the survivors are still evaluable.
//
// Every task must ship an oracle: it is the reference solution the task's own
// verifier must pass. Without one a broken task is indistinguishable from a
// broken skill. Splitting into dev/holdout and writing to disk are separate
// steps (Split, Write) so a caller can inspect the draft first.
func GenerateN(ctx context.Context, g llm.Gateway, s *spec.SkillSpec, n int) (*Suite, []Dropped, error) {
	outline, err := llm.CompleteJSON[outlineResult](ctx, g, llm.Req{
		Role:       roleOutline,
		Prompt:     outlinePrompt(s, n),
		Schema:     []byte(outlineSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("suite: outlining suite: %w", err)
	}
	if len(outline.Tasks) == 0 {
		return nil, nil, fmt.Errorf("suite: the outline named no tasks")
	}

	var (
		mu      sync.Mutex
		tasks   []TaskPkg
		dropped []Dropped
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, expandConcurrency)

	for _, stub := range outline.Tasks {
		wg.Add(1)
		go func(id, kind, intent string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			pkg, eErr := llm.CompleteJSON[expandResult](ctx, g, llm.Req{
				Role:       roleExpand,
				Prompt:     expandPrompt(s, id, kind, intent),
				Schema:     []byte(expandSchema),
				ModelClass: llm.ClassStrong,
			})

			mu.Lock()
			defer mu.Unlock()
			if eErr != nil {
				dropped = append(dropped, Dropped{TaskID: id, Kind: kind, Reason: eErr.Error()})
				return
			}
			tasks = append(tasks, TaskPkg{
				ID: id, Kind: kind,
				PromptMD: pkg.PromptMD,
				Setup:    pkg.Setup,
				Oracle:   pkg.Oracle,
				Verifier: pkg.Verifier,
			})
		}(stub.ID, stub.Kind, stub.Intent)
	}
	wg.Wait()

	if len(tasks) == 0 {
		return nil, dropped, fmt.Errorf("suite: every one of the %d outlined tasks failed to expand", len(outline.Tasks))
	}

	// Goroutine completion order is not deterministic, and Split seeds off task
	// order — an unsorted slice would make the dev/holdout split vary between
	// two drafts of the same suite.
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	sort.Slice(dropped, func(i, j int) bool { return dropped[i].TaskID < dropped[j].TaskID })

	return &Suite{
		Tasks: tasks,
		Triggers: TriggerSet{
			Positive: outline.Triggers.Positive,
			Negative: outline.Triggers.Negative,
		},
	}, dropped, nil
}

// specContext is the part of the prompt both phases share: what the skill is
// and what it is meant to do.
func specContext(s *spec.SkillSpec) string {
	var b strings.Builder
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
	return b.String()
}

// outlinePrompt asks for stubs only. Distinctness is decided here, because this
// is the one call that sees every task at once.
func outlinePrompt(s *spec.SkillSpec, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Outline an evaluation suite for the skill %q.\n\n", s.Name)
	b.WriteString(specContext(s))

	fmt.Fprintf(&b, "\nProduce exactly %d task stubs: a happy-path task, paraphrase variants, "+
		"and edge cases. Each must be meaningfully different from the others — two stubs that "+
		"test the same behaviour measure that behaviour twice and the rest not at all. "+
		"Each stub carries:\n", n)
	b.WriteString("- \"id\": a short, unique, filesystem-safe slug — it becomes a directory name.\n")
	b.WriteString("- \"kind\": one of \"happy\", \"variant\", \"edge\", \"negative-trigger\".\n")
	b.WriteString("- \"intent\": one sentence saying what this task tests. The scripts come later.\n\n")

	b.WriteString("Also produce a trigger set: 8 positive prompts that should activate this " +
		"skill, and 8 hard negatives. A hard negative is an adjacent-domain near-miss — it must " +
		"read as though it could plausibly belong to this skill's domain, without actually " +
		"matching its purpose. An obviously irrelevant negative tests nothing.\n\n")

	b.WriteString("Respond with JSON: {\"tasks\": [...], \"triggers\": {\"positive\": [...], " +
		"\"negative\": [...]}}. No prose outside the JSON.\n")
	return b.String()
}

// expandPrompt asks for one task package. Writing one task per call is what
// keeps a response bounded regardless of how many tasks the suite has.
func expandPrompt(s *spec.SkillSpec, id, kind, intent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write one evaluation task for the skill %q.\n\n", s.Name)
	b.WriteString(specContext(s))

	fmt.Fprintf(&b, "\nThe task to write:\n- id: %s\n- kind: %s\n- intent: %s\n\n", id, kind, intent)

	b.WriteString("Produce:\n")
	b.WriteString("- \"prompt_md\": the task prompt given to the agent under test.\n")
	b.WriteString("- \"setup\": optional. A shell script (written to environment/setup.sh) that " +
		"creates the task's input files, run in the workspace before the agent and before the " +
		"oracle. If the prompt refers to a file, this is the only thing that creates it — the " +
		"oracle and the verifier must read those inputs, never write them. Omit it for a task " +
		"that needs no input files. Use only ordinary shell (mkdir, heredocs, printf); it is " +
		"not a Dockerfile, and a package the task needs belongs in the skill spec's deps.\n")
	b.WriteString("- \"oracle\": a shell script (written to oracle/solve.sh) that is a reference " +
		"solution to this task. It must pass this task's own verifier — if it doesn't, the task " +
		"is void rather than the skill under test being blamed.\n")
	b.WriteString("- \"verifier\": a shell script (written to verifier/test.sh) that exits zero " +
		"only when the task was solved correctly and exits non-zero on any failure. A verifier " +
		"that cannot fail cannot detect a broken skill. Print a line beginning \"FAIL: \" naming " +
		"what was wrong before exiting non-zero — that line is what the report shows.\n\n")

	b.WriteString("Scripts run under bash. Respond with JSON: {\"prompt_md\": \"...\", " +
		"\"setup\": \"...\", \"oracle\": \"...\", \"verifier\": \"...\"}. No prose outside the JSON.\n")
	return b.String()
}
