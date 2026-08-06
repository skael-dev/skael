package report

import (
	"strings"
	"testing"
)

// The unevaluable count is per (rule × event) pair, so one systematic cause
// produces hundreds of identical lines. A real report listed 326 of them drawn
// from about a dozen distinct messages, burying the only fact a reader needs:
// how many *different* things went wrong.
func TestDedupeDetail(t *testing.T) {
	t.Run("collapses repeats and keeps first-seen order", func(t *testing.T) {
		got := dedupeDetail([]string{"b", "a", "b", "b", "c", "a"}, 25)
		want := []string{"b (×3)", "a (×2)", "c"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("nothing in, nothing out", func(t *testing.T) {
		if got := dedupeDetail(nil, 25); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	// A silently truncated list reads as a complete one, which is how a reader
	// concludes they have seen every cause.
	t.Run("says what it dropped", func(t *testing.T) {
		in := []string{"a", "b", "c", "d"}
		got := dedupeDetail(in, 2)
		if len(got) != 3 {
			t.Fatalf("got %v, want 2 reasons plus a truncation note", got)
		}
		last := got[len(got)-1]
		if !strings.Contains(last, "2") || !strings.Contains(last, "not shown") {
			t.Errorf("truncation note does not say how many were dropped: %q", last)
		}
	})

	t.Run("a single occurrence carries no count", func(t *testing.T) {
		got := dedupeDetail([]string{"only once"}, 25)
		if len(got) != 1 || got[0] != "only once" {
			t.Errorf("got %v, want the message unadorned", got)
		}
	})
}
