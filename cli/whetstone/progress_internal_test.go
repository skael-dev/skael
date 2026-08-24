package whetstone

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
)

// captureStderr runs fn with os.Stderr replaced by a pipe and returns what it
// wrote. ui writes to os.Stderr directly, so this is the only seam.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()

	fn()
	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestProgressGateway_ConcurrentCallsPrintWholeLines is why the start line was
// removed: a start/finish pair per call interleaves under concurrency, so the
// resources pass had to stay in series to keep the log readable.
func TestProgressGateway_ConcurrentCallsPrintWholeLines(t *testing.T) {
	g := &progressGateway{inner: fake.NewFunc(func(llm.Req) (string, error) { return "ok", nil })}

	out := captureStderr(t, func() {
		var wg sync.WaitGroup
		for _, role := range []string{"alpha", "bravo"} {
			wg.Add(1)
			go func(role string) {
				defer wg.Done()
				if _, err := g.Complete(context.Background(), llm.Req{Role: role, Prompt: "p"}); err != nil {
					t.Errorf("Complete: %v", err)
				}
			}(role)
		}
		wg.Wait()
	})

	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("two concurrent calls printed %d lines, want 2:\n%s", len(lines), out)
	}
	for _, want := range []string{"alpha", "bravo"} {
		if !strings.Contains(out, want+" done in") {
			t.Errorf("no whole completion line for %q:\n%s", want, out)
		}
	}
}
