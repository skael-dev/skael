package agent_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
)

// TestCaps_AuthEnvIncludesGatewayOverrides pins that AuthEnv still forwards
// ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN, and now also
// ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN — the pair that points this
// panel agent at an Anthropic-compatible gateway such as OpenRouter.
func TestCaps_AuthEnvIncludesGatewayOverrides(t *testing.T) {
	want := []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"}
	got := agent.New().Caps().AuthEnv

	for _, name := range want {
		found := false
		for _, g := range got {
			if g == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AuthEnv = %v, missing %q", got, name)
		}
	}
}
