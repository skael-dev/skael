// Package llm is the single seam through which every model call passes:
// interview, generation, suite drafting, judging, and repair. Agent CLIs do not
// expose temperature, so the determinism mitigations — schema-constrained
// prompts, tolerant extraction with one retry, and content-hash caching — live
// here rather than in each caller.
package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ModelClass routes a call to a capability tier rather than a model name, so
// gateways can map tiers onto whatever the local subscription offers.
type ModelClass string

const (
	// ClassStrong is for generation and judging.
	ClassStrong ModelClass = "strong"
	// ClassFast is for cheap classification, e.g. "did the skill trigger?".
	ClassFast ModelClass = "fast"
)

// Req is one model call.
type Req struct {
	// Role tags the call for logging and cache partitioning, e.g. "interview",
	// "gen.body", "suite.draft".
	Role       string
	Prompt     string
	Schema     json.RawMessage
	ModelClass ModelClass
	// CacheKey overrides the derived content hash. Leave empty to use CacheKey.
	CacheKey string
}

// Res is one model response.
type Res struct {
	Text   string
	Model  string
	Cached bool
}

// Gateway is a model backend.
type Gateway interface {
	Complete(ctx context.Context, r Req) (Res, error)
	// ModelFor names the concrete model that would serve a request of the
	// given class, without making a call. This exists so a caller that only
	// knows the class it asked for (the gateway resolves ModelClass to a
	// concrete model internally) can still record which model actually did
	// the work — e.g. Report.JudgeModel, which gates whether two scores are
	// comparable. A gateway that genuinely cannot know its own model (a
	// subscription CLI left to pick its own default) must return "" rather
	// than guess: an empty value means "unknown", and a guess recorded as
	// provenance would be worse than no provenance at all.
	ModelFor(class ModelClass) string
}

// Cache stores completions by content hash so a given request is never asked
// twice within or across runs. Implemented by the store package.
type Cache interface {
	Get(key string) (string, bool, error)
	Put(key, value string) error
}

// CacheKey derives a content-addressed key. Every field that changes the model's
// answer must contribute, or a cache hit serves a response to the wrong
// question — which would be invisible and would corrupt scores.
func CacheKey(r Req) string {
	if r.CacheKey != "" {
		return r.CacheKey
	}
	h := sha256.New()
	for _, part := range []string{r.Role, string(r.ModelClass), r.Prompt, string(r.Schema)} {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CompleteJSON calls the gateway and decodes a JSON response into T, retrying
// once with the parse error quoted. Two attempts, not more: a model that fails
// twice on a schema is not going to succeed on the third try, and each attempt
// costs a session from the budget.
func CompleteJSON[T any](ctx context.Context, g Gateway, r Req) (T, error) {
	var zero T

	for attempt := 0; attempt < 2; attempt++ {
		res, err := g.Complete(ctx, r)
		if err != nil {
			return zero, fmt.Errorf("llm.CompleteJSON %s: %w", r.Role, err)
		}

		raw, err := ExtractJSON(res.Text)
		if err == nil {
			var out T
			uerr := json.Unmarshal(raw, &out)
			if uerr == nil {
				return out, nil
			}
			err = uerr
		}

		if attempt == 1 {
			return zero, fmt.Errorf("llm.CompleteJSON %s: unparseable after retry: %w", r.Role, err)
		}
		r.Prompt = r.Prompt + "\n\nYour previous response could not be parsed: " +
			err.Error() + "\nReply with JSON only — no prose, no code fence."
	}
	return zero, fmt.Errorf("llm.CompleteJSON %s: unreachable", r.Role)
}
