package tune

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// noneAnswer is what the model replies when it selects no skill.
const noneAnswer = "none"

const selectSchema = `{
  "type": "object",
  "required": ["skill"],
  "properties": {"skill": {"type": "string"}}
}`

type selectResult struct {
	Skill string `json:"skill"`
}

// QueryResult is one trigger query's measurement.
type QueryResult struct {
	Query         string `json:"query"`
	ShouldTrigger bool   `json:"should_trigger"`
	Triggers      int    `json:"triggers"`
	Runs          int    `json:"runs"`
	Pass          bool   `json:"pass"`
}

// ScoreResult is a whole trigger set's measurement.
type ScoreResult struct {
	Results []QueryResult `json:"results"`
	Passed  int           `json:"passed"`
	Failed  int           `json:"failed"`
	Total   int           `json:"total"`
}

// ScoreOptions configures one Score call.
type ScoreOptions struct {
	Distractors []suite.Distractor
	Runs        int
	Threshold   float64
	Concurrency int
}

// Score measures how often a description makes a model choose this skill.
//
// One gateway call per query per run. The prompt presents the candidate skill
// beside the shipped distractor pack, so the measurement can discriminate: a
// selection against no alternatives says nothing about precision. This is a
// model's selection decision rather than an agent's, so it can disagree with
// the trigger F1 an evaluation reports. `whetstone tune --confirm` is what
// checks the winner against the real probe path.
func Score(ctx context.Context, g llm.Gateway, skillName, description string,
	set []suite.TriggerQuery, opts ScoreOptions) (ScoreResult, error) {

	if g == nil {
		return ScoreResult{}, fmt.Errorf("tune: Score requires a gateway")
	}
	if opts.Runs < 1 {
		opts.Runs = 1
	}
	if opts.Threshold <= 0 {
		opts.Threshold = 0.5
	}
	if opts.Concurrency < 1 {
		opts.Concurrency = 8
	}

	prompts := make([]string, len(set))
	for i, q := range set {
		prompts[i] = selectPrompt(skillName, description, opts.Distractors, q.Query)
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		sem      = make(chan struct{}, opts.Concurrency)
		triggers = make([]int, len(set))
		errs     []error
	)
	for i := range set {
		for run := 0; run < opts.Runs; run++ {
			wg.Add(1)
			go func(i, run int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				res, err := llm.CompleteJSON[selectResult](ctx, g, llm.Req{
					Role:   "tune.select",
					Prompt: prompts[i],
					Schema: []byte(selectSchema),
					// ClassStrong, not ClassFast, although this is a cheap
					// classification. ClassFast resolves to the last entry of
					// LLM_MODEL, which is the weakest configured model. A
					// description tuned against that one is tuned against a
					// model nobody runs the panel's lead on. The target moves
					// the day an operator adds a floor model.
					ModelClass: llm.ClassStrong,
					// The run index is part of the cache key, so two runs of
					// one query are two measurements rather than one answer
					// served twice.
					CacheKey: llm.CacheKey(llm.Req{Role: "tune.select", Prompt: prompts[i]}) + fmt.Sprintf(":%d", run),
				})
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, fmt.Errorf("query %d: %w", i, err))
					return
				}
				if strings.EqualFold(strings.TrimSpace(res.Skill), skillName) {
					triggers[i]++
				}
			}(i, run)
		}
	}
	wg.Wait()
	if len(errs) > 0 {
		return ScoreResult{}, fmt.Errorf("tune: scoring the trigger set: %w", errs[0])
	}

	out := ScoreResult{Total: len(set)}
	for i, q := range set {
		rate := float64(triggers[i]) / float64(opts.Runs)
		pass := rate >= opts.Threshold
		if !q.ShouldTrigger {
			pass = rate < opts.Threshold
		}
		if pass {
			out.Passed++
		} else {
			out.Failed++
		}
		out.Results = append(out.Results, QueryResult{
			Query: q.Query, ShouldTrigger: q.ShouldTrigger,
			Triggers: triggers[i], Runs: opts.Runs, Pass: pass,
		})
	}
	return out, nil
}

// selectPrompt asks which skill a model consults for one query.
func selectPrompt(skillName, description string, distractors []suite.Distractor, query string) string {
	var b strings.Builder
	b.WriteString("You are an agent choosing whether to consult a skill.\n\n")
	b.WriteString("These skills are available:\n\n")
	fmt.Fprintf(&b, "- %s: %s\n", skillName, description)
	for _, d := range distractors {
		fmt.Fprintf(&b, "- %s: %s\n", d.Name, d.Description)
	}
	b.WriteString("\nA skill is worth consulting only for work you cannot do directly with ordinary\n")
	b.WriteString("tools. A simple one-step request needs no skill.\n\n")
	fmt.Fprintf(&b, "The user says:\n\n%s\n\n", query)
	fmt.Fprintf(&b, "Reply with JSON only: {\"skill\": \"<name>\"} or {\"skill\": %q}.\n", noneAnswer)
	return b.String()
}
