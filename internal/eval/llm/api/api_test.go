package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/api"
)

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func gateway(t *testing.T, url string, opts ...func(*api.Options)) *api.Gateway {
	t.Helper()
	o := api.Options{
		BaseURL:     url,
		APIKey:      "sk-test",
		StrongModel: "claude-opus-5",
		FastModel:   "claude-haiku-4-5-20251001",
		MaxRetries:  2,
		Sleep:       func(time.Duration) {},
	}
	for _, f := range opts {
		f(&o)
	}
	g, err := api.New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func TestComplete_SendsAuthAndVersionHeaders(t *testing.T) {
	var gotKey, gotVersion, gotCT string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotCT = r.Header.Get("content-type")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"ok\":true}"}],"model":"claude-opus-5"}`)
	})

	if _, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotKey != "sk-test" {
		t.Errorf("x-api-key = %q", gotKey)
	}
	if gotVersion == "" {
		t.Error("anthropic-version header missing; the API requires it")
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Errorf("content-type = %q", gotCT)
	}
}

func TestComplete_ConcatenatesTextBlocks(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"a\":"},{"type":"text","text":"1}"}],"model":"m"}`)
	})

	res, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// A multi-block response must be joined, not truncated to the first block.
	if res.Text != `{"a":1}` {
		t.Errorf("Text = %q, want the blocks concatenated", res.Text)
	}
}

func TestComplete_RoutesModelClass(t *testing.T) {
	var gotModel string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{}"}],"model":"m"}`)
	})

	g := gateway(t, s.URL)
	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y", ModelClass: llm.ClassFast}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotModel != "claude-haiku-4-5-20251001" {
		t.Errorf("model = %q, want the fast model", gotModel)
	}
}

func TestComplete_RetriesServerErrorsButNotClientErrors(t *testing.T) {
	t.Run("529 is retried", func(t *testing.T) {
		var calls int32
		s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
			if atomic.AddInt32(&calls, 1) == 1 {
				w.WriteHeader(529)
				_, _ = io.WriteString(w, `{"error":{"message":"Overloaded"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"ok\":true}"}],"model":"m"}`)
		})

		res, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
		if err != nil {
			t.Fatalf("Complete did not retry a 529: %v", err)
		}
		if res.Text != `{"ok":true}` {
			t.Errorf("Text = %q", res.Text)
		}
		if calls != 2 {
			t.Errorf("made %d calls, want 2", calls)
		}
	})

	t.Run("400 is not retried", func(t *testing.T) {
		var calls int32
		s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"bad request"}}`)
		})

		if _, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
			t.Fatal("Complete succeeded on a 400")
		}
		// Retrying a malformed request just burns quota to get the same answer.
		if calls != 1 {
			t.Errorf("made %d calls for a 400, want 1", calls)
		}
	})
}

func TestComplete_SurfacesAPIErrorMessage(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"max_tokens is too large"}}`)
	})

	_, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
	if err == nil || !strings.Contains(err.Error(), "max_tokens is too large") {
		t.Errorf("err = %v, want the API's own message", err)
	}
}

func TestNew_RequiresAnAPIKey(t *testing.T) {
	if _, err := api.New(api.Options{BaseURL: "http://x"}); err != api.ErrNoAPIKey {
		t.Errorf("err = %v, want ErrNoAPIKey", err)
	}
}

func TestComplete_UsesTheCache(t *testing.T) {
	var calls int32
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"ok\":true}"}],"model":"m"}`)
	})

	c := &memCache{m: map[string]string{}}
	g := gateway(t, s.URL, func(o *api.Options) { o.Cache = c })
	req := llm.Req{Role: "gen", Prompt: "same", ModelClass: llm.ClassStrong}

	if _, err := g.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	res, err := g.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cached {
		t.Error("second identical call not served from cache")
	}
	if calls != 1 {
		t.Errorf("made %d HTTP calls for two identical requests, want 1", calls)
	}
}

// TestComplete_CacheKeyDiffersByModelClass guards against a hit that would
// serve a fast-model answer as if it came from the strong model: two requests
// that differ only in ModelClass must not share a cache entry.
func TestComplete_CacheKeyDiffersByModelClass(t *testing.T) {
	var calls int32
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"ok\":true}"}],"model":"m"}`)
	})

	c := &memCache{m: map[string]string{}}
	g := gateway(t, s.URL, func(o *api.Options) { o.Cache = c })

	strong := llm.Req{Role: "gen", Prompt: "same", ModelClass: llm.ClassStrong}
	fast := llm.Req{Role: "gen", Prompt: "same", ModelClass: llm.ClassFast}

	if _, err := g.Complete(context.Background(), strong); err != nil {
		t.Fatal(err)
	}
	res, err := g.Complete(context.Background(), fast)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cached {
		t.Error("a request differing only in ModelClass was served from the other class's cache entry")
	}
	if calls != 2 {
		t.Errorf("made %d HTTP calls for two requests differing only in ModelClass, want 2", calls)
	}
}

// TestComplete_SchemaReachesThePrompt guards against a regression that
// silently strips schema constraints from every structured call — which would
// look like the model "just stopped returning JSON" rather than an obvious
// wiring failure.
func TestComplete_SchemaReachesThePrompt(t *testing.T) {
	var gotBody []byte
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{}"}],"model":"m"}`)
	})

	schema := json.RawMessage(`{"type":"object","required":["ok"]}`)
	if _, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y", Schema: schema}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var body struct {
		Messages []struct{ Content string }
	}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if len(body.Messages) == 0 || !strings.Contains(body.Messages[0].Content, `"required":["ok"]`) {
		t.Errorf("outgoing prompt = %q, want it to contain the schema", gotBody)
	}
}

type memCache struct{ m map[string]string }

func (c *memCache) Get(k string) (string, bool, error) { v, ok := c.m[k]; return v, ok, nil }
func (c *memCache) Put(k, v string) error              { c.m[k] = v; return nil }
