package agentcli_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/agentcli"
)

// newGateway returns a gateway wired to the fake CLI, plus the argv log path.
func newGateway(t *testing.T, mode string, opts ...func(*agentcli.Options)) (*agentcli.Gateway, string) {
	t.Helper()

	bin, err := filepath.Abs(filepath.Join("testdata", "fake-claude"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}

	argv := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_CLAUDE_MODE", mode)
	t.Setenv("FAKE_CLAUDE_ARGV", argv)

	o := agentcli.Options{
		Binary:      bin,
		StrongModel: "claude-opus-5",
		FastModel:   "claude-haiku-4-5-20251001",
		Timeout:     10 * time.Second,
		MaxRetries:  2,
		Sleep:       func(time.Duration) {}, // no real waiting in tests
	}
	for _, f := range opts {
		f(&o)
	}

	g, err := agentcli.New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g, argv
}

func TestComplete_ReturnsResultText(t *testing.T) {
	g, _ := newGateway(t, "ok")

	res, err := g.Complete(context.Background(), llm.Req{Role: "interview", Prompt: "hello", ModelClass: llm.ClassStrong})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Errorf("Text = %q, want the result field's contents", res.Text)
	}
	if res.Model == "" {
		t.Error("Model empty; the resolved model is recorded per run")
	}
}

func TestComplete_PassesTheExpectedFlags(t *testing.T) {
	g, argv := newGateway(t, "ok")
	if _, err := g.Complete(context.Background(), llm.Req{Role: "gen", Prompt: "body", ModelClass: llm.ClassStrong}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	b, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("argv log: %v", err)
	}
	got := string(b)

	for _, want := range []string{"-p", "--output-format", "json", "--model", "claude-opus-5"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv missing %q:\n%s", want, got)
		}
	}
	// Gateway calls must not be able to touch the filesystem or spawn tools —
	// this is a text completion, and a gateway that can run Bash is a gateway
	// that can be prompt-injected into running Bash.
	if !strings.Contains(got, "--disallowed-tools") {
		t.Errorf("argv does not disable tools:\n%s", got)
	}
}

func TestComplete_RoutesFastClassToTheFastModel(t *testing.T) {
	g, argv := newGateway(t, "ok")
	if _, err := g.Complete(context.Background(), llm.Req{Role: "classify", Prompt: "did it trigger?", ModelClass: llm.ClassFast}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	b, _ := os.ReadFile(argv)
	if !strings.Contains(string(b), "claude-haiku-4-5-20251001") {
		t.Errorf("fast class did not route to the fast model:\n%s", b)
	}
}

func TestComplete_IsErrorIsHonouredEvenWhenSubtypeSaysSuccess(t *testing.T) {
	// The observed real failure mode: subtype "success", is_error true, and the
	// error text in result. Gating on subtype would hand "API Error: 529…" to
	// the JSON extractor and report it as a model formatting problem.
	g, _ := newGateway(t, "api_error", func(o *agentcli.Options) { o.MaxRetries = 0 })

	_, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
	if err == nil {
		t.Fatal("Complete succeeded on a response with is_error true")
	}
	if !strings.Contains(err.Error(), "529") {
		t.Errorf("error does not surface the underlying cause: %v", err)
	}
}

func TestComplete_RetriesTransientAPIErrors(t *testing.T) {
	g, _ := newGateway(t, "flaky")

	res, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
	if err != nil {
		t.Fatalf("Complete did not retry a transient error: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Errorf("Text = %q after retry", res.Text)
	}
}

func TestComplete_GarbageOutputIsAnError(t *testing.T) {
	g, _ := newGateway(t, "garbage", func(o *agentcli.Options) { o.MaxRetries = 0 })
	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Error("Complete accepted non-JSON envelope output")
	}
}

func TestComplete_UsesTheCache(t *testing.T) {
	c := &memCache{m: map[string]string{}}
	g, argv := newGateway(t, "ok", func(o *agentcli.Options) { o.Cache = c })

	req := llm.Req{Role: "gen", Prompt: "identical", ModelClass: llm.ClassStrong}
	first, err := g.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	second, err := g.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("second Complete: %v", err)
	}

	if second.Text != first.Text {
		t.Errorf("cached response differs: %q vs %q", second.Text, first.Text)
	}
	if !second.Cached {
		t.Error("second call not reported as cached")
	}

	// Sessions are the budget unit, so a cache hit must not spawn the CLI.
	b, _ := os.ReadFile(argv)
	if n := strings.Count(string(b), "-p"); n != 1 {
		t.Errorf("CLI invoked %d times for two identical requests; the cache must prevent the second", n)
	}
}

func TestComplete_ContextCancellationIsRespected(t *testing.T) {
	g, _ := newGateway(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := g.Complete(ctx, llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Error("Complete ignored a cancelled context")
	}
}

func TestNew_RejectsAMissingBinary(t *testing.T) {
	if _, err := agentcli.New(agentcli.Options{Binary: "/nonexistent/claude"}); err == nil {
		t.Error("New accepted a binary that does not exist")
	}
}

// TestComplete_TimeoutIsEnforcedEvenWhenTheCLIForksAChild reproduces a real
// bug: a CLI invocation that forks an external child (any shell script does
// this for anything it shells out to) and that child hangs. Killing only the
// direct process, as exec.CommandContext's default cancellation does, leaves
// the grandchild running and holding stdout/stderr open, so Wait blocks well
// past the configured Timeout. The select below bounds the test itself so a
// regression fails fast instead of hanging CI.
func TestComplete_TimeoutIsEnforcedEvenWhenTheCLIForksAChild(t *testing.T) {
	g, _ := newGateway(t, "hang", func(o *agentcli.Options) {
		o.Timeout = 300 * time.Millisecond
		o.MaxRetries = 0
	})

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Complete succeeded against a CLI that produced no output")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("Complete took %s to return; the timeout was not enforced", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Complete did not return within 5s; a CLI that forks a hung child blocked the call indefinitely")
	}
}

// TestComplete_RefusalMentioningTransientWordsIsNotRetried covers the false
// positive found in the original word-scan classifier: a refusal (is_error
// true, but the process itself exits 0 — the CLI still considers this a
// handled, successful invocation) whose own prose happens to mention
// "timeout" and "503" must not be classified as a transient infrastructure
// failure, or a non-retryable answer gets retried MaxRetries times for no
// benefit.
func TestComplete_RefusalMentioningTransientWordsIsNotRetried(t *testing.T) {
	g, argv := newGateway(t, "refusal")

	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Fatal("Complete succeeded on a refusal")
	}

	b, _ := os.ReadFile(argv)
	if n := strings.Count(string(b), "-p"); n != 1 {
		t.Errorf("CLI invoked %d times for a non-retryable refusal; retrying wastes a session on an answer that cannot change", n)
	}
}

// TestComplete_429IsRetried covers the false negative found in the original
// classifier: 429 was absent from the marker list even though "rate limit"
// was present. The fake CLI's rate_limited mode always fails, so a
// successful retry classification shows up as every one of MaxRetries+1
// attempts being made.
func TestComplete_429IsRetried(t *testing.T) {
	g, argv := newGateway(t, "rate_limited")

	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Fatal("Complete unexpectedly succeeded against a CLI that always reports 429")
	}

	b, _ := os.ReadFile(argv)
	// newGateway's default MaxRetries is 2, so 3 total invocations are
	// expected once every retry is exhausted.
	if n := strings.Count(string(b), "-p"); n != 3 {
		t.Errorf("CLI invoked %d times; a 429 must be retried up to MaxRetries", n)
	}
}

// TestComplete_PermanentFailureIsNotRetried covers a regression the previous
// retry-classification fix introduced: treating any non-zero exit as
// sufficient evidence of a transient failure retried a permanent
// misconfiguration (a typo'd --model here; equally a bad flag or expired
// auth) for no benefit — the identical failure forever, at the cost of two
// extra sessions per occurrence.
func TestComplete_PermanentFailureIsNotRetried(t *testing.T) {
	g, argv := newGateway(t, "bad_model")

	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Fatal("Complete succeeded against a permanently broken invocation")
	}

	b, _ := os.ReadFile(argv)
	if n := strings.Count(string(b), "-p"); n != 1 {
		t.Errorf("CLI invoked %d times for a permanent failure (bad --model); a non-zero exit alone must not be sufficient to retry", n)
	}
}

// TestComplete_QuotedAPIErrorInRefusalIsNotRetried covers the residual false
// positive in an unanchored pattern: a refusal that merely quotes the CLI's
// own "API Error: <status>" shape inside unrelated prose must not be
// mistaken for that shape actually being the result.
func TestComplete_QuotedAPIErrorInRefusalIsNotRetried(t *testing.T) {
	g, argv := newGateway(t, "quoted_error")

	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "y"}); err == nil {
		t.Fatal("Complete succeeded on a refusal")
	}

	b, _ := os.ReadFile(argv)
	if n := strings.Count(string(b), "-p"); n != 1 {
		t.Errorf("CLI invoked %d times for a refusal that only quotes an API Error shape; it must not be retried", n)
	}
}

func TestDetect_FindsABinaryOnPATH(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got, err := agentcli.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got != bin {
		t.Errorf("Detect = %q, want %q", got, bin)
	}
}

func TestDetect_ReturnsErrNoCLIWhenNothingIsOnPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, err := agentcli.Detect(); !errors.Is(err, agentcli.ErrNoCLI) {
		t.Errorf("Detect error = %v, want ErrNoCLI", err)
	}
}

type memCache struct{ m map[string]string }

func (c *memCache) Get(k string) (string, bool, error) { v, ok := c.m[k]; return v, ok, nil }
func (c *memCache) Put(k, v string) error              { c.m[k] = v; return nil }
