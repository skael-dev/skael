package fake_test

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
)

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
