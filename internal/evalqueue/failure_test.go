package evalqueue_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/evalqueue"
)

func TestExplain_NamesTheStageThatFailed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "suite generation",
			raw:  "worker: derive suite for frontend-design: derive: generate suite: suite: outlining suite: llm.CompleteJSON suite.outline: unparseable after retry",
			want: "Could not generate an evaluation suite for this skill",
		},
		{
			name: "suite too thin",
			raw:  "worker: derive suite for x: derive: the derived suite is too thin to evaluate: runner: tier full needs 7 dev tasks, the suite has 3",
			want: "too few usable tasks",
		},
		{
			name: "truncated completion",
			raw:  "derive: generate suite: suite: outlining suite: api: response truncated at max_tokens (32768)",
			want: "too long",
		},
		{
			name: "panel unhealthy",
			raw:  "worker: eval 7: panel health check failed: api: 404: model not found",
			want: "evaluation panel could not start",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalqueue.Explain(c.raw)
			if !strings.Contains(strings.ToLower(got), strings.ToLower(c.want)) {
				t.Errorf("Explain(...) = %q, want it to mention %q", got, c.want)
			}
			if got == c.raw {
				t.Error("the raw error chain was returned unchanged")
			}
		})
	}
}

// An unrecognised failure must not be swallowed or replaced by a vague
// placeholder — an operator still needs to see it.
func TestExplain_PassesAnUnrecognisedErrorThrough(t *testing.T) {
	raw := "worker: something nobody has classified yet"
	if got := evalqueue.Explain(raw); got != raw {
		t.Errorf("Explain(%q) = %q, want it returned unchanged", raw, got)
	}
}
