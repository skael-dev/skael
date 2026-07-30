package agentcli_test

import (
	"context"
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

type memCache struct{ m map[string]string }

func (c *memCache) Get(k string) (string, bool, error) { v, ok := c.m[k]; return v, ok, nil }
func (c *memCache) Put(k, v string) error              { c.m[k] = v; return nil }
