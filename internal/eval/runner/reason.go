package runner

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	// reasonTailBytes bounds what is kept from a verifier's output. The
	// FAIL: line is the last thing a verifier prints before exiting, so
	// keeping the tail keeps the part that says why.
	reasonTailBytes = 8 << 10
	// reasonMaxLen caps the distilled one-liner, in runes.
	reasonMaxLen = 160
)

// tailWriter keeps only the last reasonTailBytes written to it, so a verifier
// that prints a megabyte of diff costs a fixed amount of memory per run.
type tailWriter struct{ buf []byte }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > reasonTailBytes {
		// Re-copy rather than reslice: a reslice keeps the whole grown
		// backing array alive, which is the allocation this cap exists to
		// avoid.
		t.buf = append([]byte(nil), t.buf[len(t.buf)-reasonTailBytes:]...)
	}
	return len(p), nil
}

func (t *tailWriter) Bytes() []byte { return t.buf }

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// sanitizeLine removes escape sequences and control characters. Verifier
// output is model-authored and is printed to a terminal and embedded in an
// HTML page, so neither may carry cursor movement or colour of its choosing.
func sanitizeLine(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// distilReason reduces a verifier's captured output to the one line that says
// why it failed: the last line beginning "FAIL:", which is the convention
// every generated verifier follows from both shell (fail(){ echo "FAIL: $*"; })
// and Python (die()). Output that follows no convention falls back to its last
// non-empty line, which is where a traceback puts the exception.
func distilReason(out []byte) string {
	lines := strings.Split(string(out), "\n")

	reason := ""
	for _, line := range lines {
		clean := strings.TrimSpace(sanitizeLine(line))
		if strings.HasPrefix(clean, "FAIL:") {
			reason = strings.TrimSpace(strings.TrimPrefix(clean, "FAIL:"))
		}
	}
	if reason == "" {
		for i := len(lines) - 1; i >= 0; i-- {
			if clean := strings.TrimSpace(sanitizeLine(lines[i])); clean != "" {
				reason = clean
				break
			}
		}
	}

	runes := []rune(reason)
	if len(runes) > reasonMaxLen {
		return string(runes[:reasonMaxLen-1]) + "…"
	}
	return reason
}
