package llm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
)

type payload struct {
	Name string `json:"name"`
}

func TestCompleteJSON_ParsesFencedResponse(t *testing.T) {
	g := fake.New("```json\n{\"name\":\"pdf-extract\"}\n```")

	got, err := llm.CompleteJSON[payload](context.Background(), g, llm.Req{Role: "interview", Prompt: "go"})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if got.Name != "pdf-extract" {
		t.Errorf("Name = %q, want pdf-extract", got.Name)
	}
	if n := len(g.Calls()); n != 1 {
		t.Errorf("made %d gateway calls, want 1", n)
	}
}

func TestCompleteJSON_RetriesOnceQuotingTheParseError(t *testing.T) {
	g := fake.New("not json at all", `{"name":"ok"}`)

	got, err := llm.CompleteJSON[payload](context.Background(), g, llm.Req{Role: "interview", Prompt: "go"})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if got.Name != "ok" {
		t.Errorf("Name = %q, want ok", got.Name)
	}

	calls := g.Calls()
	if len(calls) != 2 {
		t.Fatalf("made %d calls, want 2 (one retry)", len(calls))
	}
	// The retry must quote what went wrong, otherwise the model has no more
	// information than it had on the attempt that already failed.
	if calls[1].Prompt == calls[0].Prompt {
		t.Error("retry prompt is identical to the first; it must quote the parse error")
	}
}

func TestCompleteJSON_GivesUpAfterOneRetry(t *testing.T) {
	g := fake.New("nope", "still nope", `{"name":"never reached"}`)

	if _, err := llm.CompleteJSON[payload](context.Background(), g, llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Fatal("CompleteJSON succeeded after two unparseable responses")
	}
	if n := len(g.Calls()); n != 2 {
		t.Errorf("made %d calls, want exactly 2", n)
	}
}

func TestCompleteJSON_PropagatesGatewayError(t *testing.T) {
	sentinel := errors.New("rate limited")
	g := fake.New()
	g.SetError(sentinel)

	if _, err := llm.CompleteJSON[payload](context.Background(), g, llm.Req{Role: "x", Prompt: "y"}); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want it to wrap %v", err, sentinel)
	}
}

func TestCacheKey_IsContentAddressedAndDiscriminating(t *testing.T) {
	base := llm.Req{Role: "generate", Prompt: "body pass", ModelClass: llm.ClassStrong}

	key1 := llm.CacheKey(base)
	key2 := llm.CacheKey(base)
	if key1 != key2 {
		t.Error("CacheKey is not stable for identical requests")
	}
	for name, mutate := range map[string]func(llm.Req) llm.Req{
		"prompt":      func(r llm.Req) llm.Req { r.Prompt = "different"; return r },
		"role":        func(r llm.Req) llm.Req { r.Role = "judge"; return r },
		"model class": func(r llm.Req) llm.Req { r.ModelClass = llm.ClassFast; return r },
		"schema":      func(r llm.Req) llm.Req { r.Schema = []byte(`{"type":"object"}`); return r },
	} {
		if llm.CacheKey(base) == llm.CacheKey(mutate(base)) {
			t.Errorf("CacheKey ignores %s — a cache hit would serve the wrong response", name)
		}
	}
}

// TestCacheKey_LengthPrefixingPreventsFieldConcatenationCollision proves the
// length-prefixing in CacheKey earns its place. Without it, Role: "ab",
// Prompt: "c" and Role: "a", Prompt: "bc" concatenate to the same bytes ("abc")
// and would hash to the same key — a cache hit silently serving the wrong
// cached answer to a different request.
func TestCacheKey_LengthPrefixingPreventsFieldConcatenationCollision(t *testing.T) {
	a := llm.Req{Role: "ab", Prompt: "c"}
	b := llm.Req{Role: "a", Prompt: "bc"}

	if llm.CacheKey(a) == llm.CacheKey(b) {
		t.Error("CacheKey collides on field-boundary-shifted requests; length-prefixing is missing")
	}
}
