package agent_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
)

func TestRegistry_ClaudeCodeIsRegistered(t *testing.T) {
	a, ok := agent.Get("claude-code")
	if !ok {
		t.Fatal("adapter \"claude-code\" not registered")
	}
	if a.Name() != "claude-code" {
		t.Errorf("adapter registered as \"claude-code\" reports Name() = %q", a.Name())
	}
}

func TestRegistry_EveryAdapterDeclaresCaps(t *testing.T) {
	// A stub with an empty SkillDir would install a bundle at the workspace
	// root and silently measure nothing.
	for _, a := range agent.All() {
		c := a.Caps()
		if c.SkillDir == "" {
			t.Errorf("%s: Caps.SkillDir is empty", a.Name())
		}
		if c.ModelFlag == "" {
			t.Errorf("%s: Caps.ModelFlag is empty", a.Name())
		}
		if c.EventTier == "" {
			t.Errorf("%s: Caps.EventTier is empty", a.Name())
		}
	}
}

func TestRegistry_GetUnknownIsNotFound(t *testing.T) {
	if _, ok := agent.Get("nope"); ok {
		t.Error("Get returned ok for an unregistered adapter")
	}
}
