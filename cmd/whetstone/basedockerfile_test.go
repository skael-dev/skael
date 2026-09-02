package main

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// The published image and the locally built one must come from the same bytes.
// A Dockerfile copied into a workflow drifts, and a base image that changes
// underneath makes two scores incomparable without saying so.
func TestPrintBaseDockerfile_EmitsImagespecsOwnBytes(t *testing.T) {
	for _, slim := range []bool{false, true} {
		got := printBaseDockerfile(slim)
		if got != imagespec.BaseDockerfile(slim) {
			t.Errorf("slim=%v: output does not match imagespec.BaseDockerfile", slim)
		}
		// The real base/Dockerfile opens with an explanatory comment block, so
		// this checks for a FROM line anywhere rather than as the first line.
		if !strings.Contains(got, "\nFROM ") && !strings.HasPrefix(got, "FROM ") {
			t.Errorf("slim=%v: output is not a Dockerfile: %.40q", slim, got)
		}
	}
}
