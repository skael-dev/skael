package agent_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
	_ "github.com/skael-dev/skael/internal/eval/agent/claudecode"
	_ "github.com/skael-dev/skael/internal/eval/agent/codex"
	_ "github.com/skael-dev/skael/internal/eval/agent/cursor"
	_ "github.com/skael-dev/skael/internal/eval/agent/opencode"
)

func TestRegistry_AllAdaptersRegistered(t *testing.T) {
	for _, name := range []string{"claude-code", "cursor", "codex", "opencode"} {
		a, ok := agent.Get(name)
		if !ok {
			t.Errorf("adapter %q not registered", name)
			continue
		}
		if a.Name() != name {
			t.Errorf("adapter registered as %q reports Name() = %q", name, a.Name())
		}
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

func TestRegistry_UnimplementedAdaptersFailClosed(t *testing.T) {
	// An unimplemented adapter must return a sentinel, never an empty result.
	// A stub Parse returning (&Result{}, nil) reads downstream as a session in
	// which the agent did nothing — a zero score for a working skill.
	for _, name := range []string{"codex", "opencode", "cursor"} {
		a, ok := agent.Get(name)
		if !ok {
			t.Fatalf("adapter %q not registered", name)
		}
		if _, err := a.Parse(nil); err != agent.ErrParseNotImplemented {
			t.Errorf("%s: Parse err = %v, want ErrParseNotImplemented", name, err)
		}
		if _, err := a.Invoke(context.TODO(), agent.InvokeSpec{}); err != agent.ErrInvokeNotImplemented {
			t.Errorf("%s: Invoke err = %v, want ErrInvokeNotImplemented", name, err)
		}
		if err := a.InstallSkill("", ""); err != agent.ErrInstallNotImplemented {
			t.Errorf("%s: InstallSkill err = %v, want ErrInstallNotImplemented", name, err)
		}
	}
}

func TestRegistry_GetUnknownIsNotFound(t *testing.T) {
	if _, ok := agent.Get("nope"); ok {
		t.Error("Get returned ok for an unregistered adapter")
	}
}

func TestRegistry_UnparsableAdaptersCannotClaimSkillInvocation(t *testing.T) {
	// An adapter that cannot parse a stream cannot possibly detect a skill invocation
	// in that stream. Claiming SupportsSkillInvocation: true for an adapter
	// returning ErrParseNotImplemented would silently zero the trigger-measurement
	// metric, which is the highest-weighted scoring pillar.
	for _, name := range []string{"codex", "opencode", "cursor"} {
		a, ok := agent.Get(name)
		if !ok {
			t.Fatalf("adapter %q not registered", name)
		}
		_, err := a.Parse(nil)
		if err == agent.ErrParseNotImplemented && a.Caps().SupportsSkillInvocation {
			t.Errorf("%s: cannot parse (ErrParseNotImplemented) but claims SupportsSkillInvocation=true", a.Name())
		}
	}
}
