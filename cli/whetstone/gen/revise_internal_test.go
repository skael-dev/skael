package gen

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/lint"
)

// TestRouteFindings_EveryRevisableErrorIsRouted pins the rules a revision
// pass can act on. An error rule missing from either map makes routeFindings
// report fixable=false, so the loop breaks without spending the call that
// would have cleared it — silently, and only for the skills unlucky enough to
// trip that rule. Names are matched against lint's literals, so a rename
// there fails here rather than degrading the loop in production.
func TestRouteFindings_EveryRevisableErrorIsRouted(t *testing.T) {
	tests := []struct {
		rule string
		want string
	}{
		{"body-token-budget", "body"},
		{"body-too-long", "body"},
		{"hedge-word", "body"},
		{"step-without-postcondition", "body"},
		{"no-terminal-fallback", "body"},
		{"global-only-guardrail", "body"},
		{"broken-link", "body"},
		{"metadata-token-budget", "frontmatter"},
		{"description-no-trigger", "frontmatter"},
		{"description-missing", "frontmatter"},
		{"description-too-long", "frontmatter"},
		// Neither pass writes these, so a revision call cannot clear them.
		{"too-many-modules", "neither"},
		{"symlink-not-allowed", "neither"},
		{"name-dir-mismatch", "neither"},
	}

	for _, tt := range tests {
		f := lint.Finding{Rule: tt.rule, Severity: lint.SeverityError}
		body, frontmatter, fixable := routeFindings([]lint.Finding{f})

		got := "neither"
		switch {
		case len(body) == 1:
			got = "body"
		case len(frontmatter) == 1:
			got = "frontmatter"
		}
		if got != tt.want {
			t.Errorf("%s routed to %s, want %s", tt.rule, got, tt.want)
		}
		if want := tt.want != "neither"; fixable != want {
			t.Errorf("%s fixable = %v, want %v", tt.rule, fixable, want)
		}
	}
}

// TestRouteFindings_WarningsAloneAreNotFixable guards the loop's exit
// condition: warnings ride along with a revision that is already happening,
// but must never be the reason one starts.
func TestRouteFindings_WarningsAloneAreNotFixable(t *testing.T) {
	body, _, fixable := routeFindings([]lint.Finding{
		{Rule: "step-without-postcondition", Severity: lint.SeverityWarn},
	})
	if fixable {
		t.Error("a warn-only finding set started a revision pass")
	}
	if len(body) != 1 {
		t.Errorf("warn finding was dropped from the body bucket: %v", body)
	}
}
