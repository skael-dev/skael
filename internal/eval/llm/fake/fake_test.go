package fake_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
)

func TestNewFunc_AnswersByRequestNotByCallOrder(t *testing.T) {
	g := fake.NewFunc(func(r llm.Req) (string, error) {
		if strings.Contains(r.Prompt, "alpha") {
			return "A", nil
		}
		return "B", nil
	})

	// Concurrent calls: with order-based scripting the answers would be
	// whichever goroutine won the lock, not which prompt was sent.
	var wg sync.WaitGroup
	got := make([]string, 2)
	for i, p := range []string{"beta", "alpha"} {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			res, err := g.Complete(context.Background(), llm.Req{Prompt: p})
			if err != nil {
				t.Errorf("Complete(%s): %v", p, err)
				return
			}
			got[i] = res.Text
		}(i, p)
	}
	wg.Wait()

	if got[0] != "B" || got[1] != "A" {
		t.Errorf("got %v, want [B A] — replies did not follow the request", got)
	}
}

func TestNewFunc_PropagatesTheFunctionsError(t *testing.T) {
	want := errors.New("boom")
	g := fake.NewFunc(func(llm.Req) (string, error) { return "", want })

	if _, err := g.Complete(context.Background(), llm.Req{Prompt: "x"}); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestNewFunc_StillRecordsCalls(t *testing.T) {
	g := fake.NewFunc(func(llm.Req) (string, error) { return "{}", nil })
	if _, err := g.Complete(context.Background(), llm.Req{Role: "r1", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	calls := g.Calls()
	if len(calls) != 1 || calls[0].Role != "r1" {
		t.Errorf("Calls() = %+v, want one call with role r1", calls)
	}
}

// The order-based constructor is used by roughly ten existing tests and must
// keep behaving exactly as it did.
func TestNew_StillRepliesInOrder(t *testing.T) {
	g := fake.New("first", "second")
	for _, want := range []string{"first", "second"} {
		res, err := g.Complete(context.Background(), llm.Req{Prompt: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Text != want {
			t.Errorf("Text = %q, want %q", res.Text, want)
		}
	}
	if _, err := g.Complete(context.Background(), llm.Req{Prompt: "x"}); err == nil {
		t.Error("a call past the scripted responses should error")
	}
}

// TestGateway_ErrorsOnceResponsesAreExhausted covers a path every other test
// in this package skips: every scripted-response scenario stays within
// len(responses), and SetError short-circuits before the guard runs. Without
// this test an off-by-one or an index panic in the guard would ship silently
// and surface later as a confusing failure inside some other task's tests.
func TestGateway_ErrorsOnceResponsesAreExhausted(t *testing.T) {
	g := fake.New("only one scripted response")

	if _, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "first"}); err != nil {
		t.Fatalf("first call: %v", err)
	}

	_, err := g.Complete(context.Background(), llm.Req{Role: "x", Prompt: "second"})
	if err == nil {
		t.Fatal("second call succeeded; want an exhaustion error since only one response was scripted")
	}
	// The error must name the call number and the scripted count, so a
	// failure here (or in a caller relying on the fake) is diagnosable
	// without stepping through the fake's internals.
	if !strings.Contains(err.Error(), "call 2") {
		t.Errorf("exhaustion error does not name the call number: %v", err)
	}
	if !strings.Contains(err.Error(), "only 1 responses scripted") {
		t.Errorf("exhaustion error does not name the scripted count: %v", err)
	}
}

func TestModelFor_IsDeterministicPerClass(t *testing.T) {
	g := fake.New()
	if got := g.ModelFor(llm.ClassStrong); got != "fake-strong" {
		t.Errorf("ModelFor(ClassStrong) = %q, want %q", got, "fake-strong")
	}
	if got := g.ModelFor(llm.ClassFast); got != "fake-fast" {
		t.Errorf("ModelFor(ClassFast) = %q, want %q", got, "fake-fast")
	}
	// An unrecognized class falls back to the strong model, the same
	// convention the real gateways follow.
	if got := g.ModelFor(llm.ModelClass("weird")); got != "fake-strong" {
		t.Errorf("ModelFor(unknown) = %q, want the strong-model fallback %q", got, "fake-strong")
	}
}
