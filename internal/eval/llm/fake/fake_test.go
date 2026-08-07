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
