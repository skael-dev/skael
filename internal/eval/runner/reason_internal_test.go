package runner

import (
	"strings"
	"testing"
)

func TestDistilReason_PrefersTheLastFailLine(t *testing.T) {
	out := []byte("checking artifacts\nFAIL: missing artifact out/table.md\nFAIL: row_count should be 2\n")
	if got := distilReason(out); got != "row_count should be 2" {
		t.Errorf("distilReason = %q, want the last FAIL line without its prefix", got)
	}
}

func TestDistilReason_FallsBackToTheLastNonEmptyLine(t *testing.T) {
	out := []byte("Traceback (most recent call last):\n  KeyError: 'dialect'\n\n")
	if got := distilReason(out); got != "KeyError: 'dialect'" {
		t.Errorf("distilReason = %q, want the last non-empty line", got)
	}
}

// Verifier output is model-authored and lands in a terminal and an HTML page,
// so escape sequences and control characters must not survive.
func TestDistilReason_StripsAnsiAndControlCharacters(t *testing.T) {
	out := []byte("\x1b[31mFAIL:\x1b[0m bad\x07 value\r\n")
	got := distilReason(out)
	if got != "bad value" {
		t.Errorf("distilReason = %q, want %q", got, "bad value")
	}
	if strings.ContainsAny(got, "\x1b\x07\r") {
		t.Errorf("distilReason left control characters in %q", got)
	}
}

func TestDistilReason_TruncatesToTheCap(t *testing.T) {
	got := distilReason([]byte("FAIL: " + strings.Repeat("x", 500)))
	if len([]rune(got)) != reasonMaxLen {
		t.Errorf("length = %d runes, want %d", len([]rune(got)), reasonMaxLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated reason %q does not end in an ellipsis", got)
	}
}

func TestDistilReason_EmptyOutputYieldsNoReason(t *testing.T) {
	if got := distilReason(nil); got != "" {
		t.Errorf("distilReason(nil) = %q, want empty", got)
	}
}

// The buffer is what bounds memory when a verifier prints megabytes. The
// FAIL: line is the last thing it prints, so the tail is the half to keep.
func TestTailWriter_KeepsTheTailWithinTheCap(t *testing.T) {
	var w tailWriter
	for i := 0; i < 100; i++ {
		if _, err := w.Write([]byte(strings.Repeat("a", 1000))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Write([]byte("\nFAIL: the tail survived\n")); err != nil {
		t.Fatal(err)
	}
	if len(w.Bytes()) > reasonTailBytes {
		t.Errorf("buffer grew to %d bytes, over the %d cap", len(w.Bytes()), reasonTailBytes)
	}
	if got := distilReason(w.Bytes()); got != "the tail survived" {
		t.Errorf("distilReason = %q; the tail was lost", got)
	}
}
