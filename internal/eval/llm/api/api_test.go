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

// TestComplete_AuthStyleAnthropicIsTheDefault pins that leaving AuthStyle
// unset behaves exactly like AuthStyleAnthropic — no bearer header, and
// anthropic-version still sent — so existing callers see no change.
func TestComplete_AuthStyleAnthropicIsTheDefault(t *testing.T) {
	var gotKey, gotVersion, gotAuth string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{}"}],"model":"m"}`)
	})

	if _, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotKey != "sk-test" {
		t.Errorf("x-api-key = %q, want it sent by default", gotKey)
	}
	if gotVersion == "" {
		t.Error("anthropic-version missing under the default auth style")
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want it unset under the default auth style", gotAuth)
	}
}

// TestComplete_AuthStyleBearerSendsAuthorizationOnly pins the OpenRouter-
// compatible auth path: Authorization: Bearer <key>, and neither x-api-key
// nor anthropic-version, since a version header meant for Anthropic's own
// API may not be understood by a different vendor's gateway.
func TestComplete_AuthStyleBearerSendsAuthorizationOnly(t *testing.T) {
	var gotAuth, gotKey, gotVersion string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{}"}],"model":"m"}`)
	})

	g := gateway(t, s.URL, func(o *api.Options) { o.AuthStyle = api.AuthStyleBearer })
	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-test")
	}
	if gotKey != "" {
		t.Errorf("x-api-key = %q, want it unset under the bearer auth style", gotKey)
	}
	if gotVersion != "" {
		t.Errorf("anthropic-version = %q, want it unset under the bearer auth style", gotVersion)
	}
}

// TestNew_DefaultsAreUnchanged pins that New with an empty Options (besides
// the required key) still yields today's defaults, so adding AuthStyle,
// BaseURL, and model overrides does not silently change existing behaviour.
func TestNew_DefaultsAreUnchanged(t *testing.T) {
	var gotKey, gotVersion, gotAuth, gotURL string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotAuth = r.Header.Get("Authorization")
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotURL = body.Model
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{}"}],"model":"m"}`)
	})

	g, err := api.New(api.Options{BaseURL: s.URL, APIKey: "sk-test", Sleep: func(time.Duration) {}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotKey != "sk-test" {
		t.Errorf("x-api-key = %q", gotKey)
	}
	if gotVersion == "" {
		t.Error("anthropic-version missing with default Options")
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want unset with default Options", gotAuth)
	}
	if gotURL != "claude-opus-5" {
		t.Errorf("model = %q, want the default strong model claude-opus-5", gotURL)
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

// TestComplete_MalformedSuccessBodyIsAnError guards against the failure class
// this project treats as most serious: a silent wrong answer rather than a
// loud failure. A 200 whose body is not valid JSON — a truncating proxy, an
// HTML error page served with a 200, a partial write — must not come back as
// a nil error with an empty Text, indistinguishable from "the model
// legitimately returned nothing".
//
// A failed decode also leaves the response zero-valued, which means the
// separate zero-text-blocks check (see TestComplete_NoTextBlocksIsAnError)
// would also produce a non-nil error here — so "err != nil" alone does not
// pin this check specifically. The assertions below require the malformed-
// body error's own signature (it quotes the offending body) and require the
// absence of the zero-blocks check's signature, so the two tests can only
// pass together if each check is doing its own, distinct job.
func TestComplete_MalformedSuccessBodyIsAnError(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html>not json</html>`)
	})

	_, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
	if err == nil {
		t.Fatal("Complete succeeded on a malformed 200 body")
	}
	if !strings.Contains(err.Error(), "not json") {
		t.Errorf("err = %v, want it to quote the malformed body", err)
	}
	if strings.Contains(err.Error(), "no text content blocks") {
		t.Errorf("err = %v, misattributed to the zero-text-blocks check instead of the decode failure", err)
	}
}

// TestComplete_NoTextBlocksIsAnError pins the decision that a well-formed 200
// with zero text content blocks is reported as an error, not a valid empty
// completion — the caller asked for a completion and got none.
//
// See the discussion on TestComplete_MalformedSuccessBodyIsAnError: this
// asserts the zero-blocks error's own signature (it names the empty-content
// condition) and the absence of the malformed-body check's signature, so a
// mutation that quietly drops this check cannot hide behind the other one.
func TestComplete_NoTextBlocksIsAnError(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"content":[],"model":"m"}`)
	})

	_, err := gateway(t, s.URL).Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
	if err == nil {
		t.Fatal("Complete succeeded on a 200 with zero text content blocks")
	}
	if !strings.Contains(err.Error(), "no text content blocks") {
		t.Errorf("err = %v, want it to name the empty-content condition", err)
	}
	if strings.Contains(err.Error(), "malformed") {
		t.Errorf("err = %v, misattributed to the malformed-body check instead of the empty-content check", err)
	}
}

// TestComplete_MalformedSuccessBodyIsRetried pins the retry classification
// for a malformed 200 body: it reads as a transport-level anomaly (a
// truncating proxy, a partial write), not a considered answer, so it is
// worth another attempt.
func TestComplete_MalformedSuccessBodyIsRetried(t *testing.T) {
	var calls int32
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `<html>not json</html>`)
	})

	g := gateway(t, s.URL, func(o *api.Options) { o.MaxRetries = 2 })
	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Fatal("Complete succeeded on a persistently malformed 200 body")
	}
	if calls <= 1 {
		t.Errorf("made %d calls for a malformed 200 body, want more than 1 — it should be retried", calls)
	}
	if calls > 3 { // MaxRetries + 1
		t.Errorf("made %d calls, want at most MaxRetries+1 = 3", calls)
	}
}

// TestComplete_NoTextBlocksIsNotRetried pins the retry classification for a
// well-formed 200 with no text: the server gave a considered, if
// content-free, reply — retrying it would only spend quota to hear the same
// answer again.
func TestComplete_NoTextBlocksIsNotRetried(t *testing.T) {
	var calls int32
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `{"content":[],"model":"m"}`)
	})

	g := gateway(t, s.URL, func(o *api.Options) { o.MaxRetries = 2 })
	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Fatal("Complete succeeded on a 200 with zero text content blocks")
	}
	if calls != 1 {
		t.Errorf("made %d calls for a zero-text-blocks 200, want exactly 1 — it should not be retried", calls)
	}
}

// TestComplete_TrimsBaseURLTrailingSlash pins the fix that prevents
// "//v1/messages": a BaseURL ending in "/" must not double the slash.
func TestComplete_TrimsBaseURLTrailingSlash(t *testing.T) {
	var gotPath string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{}"}],"model":"m"}`)
	})

	g := gateway(t, s.URL+"/")
	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %q, want exactly /v1/messages", gotPath)
	}
}

// TestComplete_MaxRetriesZeroMakesExactlyOneCall pins MaxRetries: 0 as "try
// once, no retry loop" rather than an off-by-one that still retries, and
// checks the underlying error message is not lost inside the wrapping.
func TestComplete_MaxRetriesZeroMakesExactlyOneCall(t *testing.T) {
	var calls int32
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	})

	g := gateway(t, s.URL, func(o *api.Options) { o.MaxRetries = 0 })
	_, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
	if err == nil {
		t.Fatal("Complete succeeded on a persistent 500")
	}
	if calls != 1 {
		t.Errorf("made %d calls with MaxRetries: 0, want 1", calls)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the underlying message to survive wrapping", err)
	}
}

// recordingRoundTripper wraps a real transport and records whether it was
// invoked, so a test can assert the caller-supplied HTTPClient — not some
// other, unconfigured client — is what actually made the request.
type recordingRoundTripper struct {
	http.RoundTripper
	called int32
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&rt.called, 1)
	return rt.RoundTripper.RoundTrip(req)
}

// TestComplete_HonoursHTTPClientTimeout pins that a hung server returns a
// transport error promptly rather than blocking forever — the sibling
// agentcli gateway had a genuine hang bug on its equivalent path, so this is
// worth pinning rather than assuming the stdlib client covers it by default.
//
// Two realistic regressions would still make an earlier version of this test
// pass: calling Do on a bare &http.Client{} that ignores Options.HTTPClient
// entirely, or New() unconditionally overwriting a caller-supplied HTTPClient
// with its own default. Both used to return an error within the test's time
// bound — but only because the handler gave up on its own short timer, not
// because any timeout actually fired. That gap is closed two ways: the
// handler now blocks on a channel closed only in t.Cleanup (so nothing but a
// real client-side timeout can make Complete return), and a recording
// RoundTripper proves the caller-supplied client is the one actually used.
//
// Complete runs in a goroutine, selected against a short local deadline,
// rather than being called inline: this repo's `just test` runs `go test
// ./... -count=1` with no -timeout flag, so if either regression above ever
// reintroduces a genuine hang, calling Complete directly would deadlock this
// test — and with no harness -timeout to kill it, Go's ten-minute per-package
// default would be what finally reports the failure, which is worse than the
// weak test it would be replacing. Fail fast and cleanly instead: this
// deadline is well above the 50ms client timeout (the honoured path returns
// in that ~50ms) and far below anything a CI harness would notice.
func TestComplete_HonoursHTTPClientTimeout(t *testing.T) {
	release := make(chan struct{})
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		<-release // never returns on its own; only the test's cleanup frees it
	})
	// Registered after newServer's own t.Cleanup(s.Close), so it runs first
	// (cleanups are LIFO) — the handler is released before Close is asked to
	// wait for it, and the server's own Close can never stall the suite.
	t.Cleanup(func() { close(release) })

	rt := &recordingRoundTripper{RoundTripper: http.DefaultTransport}
	g := gateway(t, s.URL, func(o *api.Options) {
		o.HTTPClient = &http.Client{Timeout: 50 * time.Millisecond, Transport: rt}
		// A single attempt is enough to prove the timeout is honoured.
		o.MaxRetries = 0
	})

	type result struct {
		err     error
		elapsed time.Duration
	}
	// Buffered so the goroutine can always send and exit, even if the select
	// below times out first — it cannot leak waiting for a reader.
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		_, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
		done <- result{err: err, elapsed: time.Since(start)}
	}()

	select {
	case res := <-done:
		if res.err == nil {
			t.Fatal("Complete succeeded against a server that never responds")
		}
		if res.elapsed > time.Second {
			t.Errorf("Complete took %s against a 50ms client timeout, want it to return promptly", res.elapsed)
		}
		lower := strings.ToLower(res.err.Error())
		if !strings.Contains(lower, "timeout") && !strings.Contains(lower, "deadline exceeded") {
			t.Errorf("err = %v, want a timeout-shaped error", res.err)
		}
		if atomic.LoadInt32(&rt.called) == 0 {
			t.Error("the caller-supplied HTTPClient's Transport was never invoked — Options.HTTPClient must be the client actually used")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Complete did not return within 500ms of a 50ms client timeout — it is blocking instead of honouring the timeout")
	}
}

type memCache struct{ m map[string]string }

func (c *memCache) Get(k string) (string, bool, error) { v, ok := c.m[k]; return v, ok, nil }
func (c *memCache) Put(k, v string) error              { c.m[k] = v; return nil }
