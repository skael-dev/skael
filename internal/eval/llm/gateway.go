// Package llm is the single seam through which every model call passes.
// Determinism mitigations (schema-constrained prompts, tolerant extraction
// with one retry, content-hash caching) live here rather than in each caller.
package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrTimeout reports that a gateway call exceeded its configured timeout,
// distinct from context cancellation.
var ErrTimeout = errors.New("llm: gateway timed out")

// ModelClass routes a call to a capability tier rather than a model name.
type ModelClass string

const (
	ClassStrong ModelClass = "strong"
	ClassFast   ModelClass = "fast"
)

// Req is one model call.
type Req struct {
	Role       string
	Prompt     string
	Schema     json.RawMessage
	ModelClass ModelClass
	CacheKey   string
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
	// ModelFor names the concrete model for a class without making a call.
	// Returns "" when unknown (e.g. a subscription CLI).
	ModelFor(class ModelClass) string
}

// Cache stores completions by content hash.
type Cache interface {
	Get(key string) (string, bool, error)
	Put(key, value string) error
}

// CacheKey derives a content-addressed key. Every field that changes the
// model's answer must contribute, or a cache hit serves a wrong response.
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
// once on a parse failure. Returns the Res alongside the decoded value so a
// caller can read the model that answered (for provenance).
func CompleteJSON[T any](ctx context.Context, g Gateway, r Req) (T, Res, error) {
	var zero T

	for attempt := 0; attempt < 2; attempt++ {
		res, err := g.Complete(ctx, r)
		if err != nil {
			return zero, Res{}, fmt.Errorf("llm.CompleteJSON %s: %w", r.Role, err)
		}

		raw, err := ExtractJSON(res.Text)
		if err == nil {
			var out T
			uerr := json.Unmarshal(raw, &out)
			if uerr == nil {
				return out, res, nil
			}
			err = uerr
		}

		if attempt == 1 {
			return zero, Res{}, fmt.Errorf("llm.CompleteJSON %s: unparseable after retry: %w", r.Role, err)
		}
		r.Prompt = r.Prompt + "\n\nYour previous response could not be parsed: " +
			err.Error() + "\nReply with JSON only — no prose, no code fence."
	}
	return zero, Res{}, fmt.Errorf("llm.CompleteJSON %s: unreachable", r.Role)
}
