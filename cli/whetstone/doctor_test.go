package whetstone_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/agent"
)

// TestRunDoctor_ReportsAGatewayWithNothingInstalled is the whole point of
// doctor: it is run when something is already wrong, so a missing agent CLI
// must be reported in the output rather than returned as an error.
func TestRunDoctor_ReportsAGatewayWithNothingInstalled(t *testing.T) {
	// An empty PATH makes every LookPath fail, so neither an agent CLI nor
	// docker is found — without shelling out to anything real.
	t.Setenv("PATH", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	rep, err := whetstone.RunDoctor(context.Background(), false)
	if err != nil {
		t.Fatalf("RunDoctor returned an error for a missing CLI: %v", err)
	}
	if rep == nil {
		t.Fatal("RunDoctor returned a nil report")
	}
	if rep.Gateway == "" {
		t.Error("report names no gateway; doctor must say which gateway would serve calls even when none can")
	}
	if rep.Gateway != "none" {
		t.Errorf("gateway = %q, want %q with no agent CLI and no API key", rep.Gateway, "none")
	}
	if rep.GatewayDetail == "" {
		t.Error("an unusable gateway must be explained, not just named")
	}
	if rep.AgentCLI != "" {
		t.Errorf("agent CLI = %q, want empty with an empty PATH", rep.AgentCLI)
	}
	if rep.Docker {
		t.Error("docker reported present with an empty PATH")
	}
}

// TestRunDoctor_ReportsTheAPIGatewayWhenAKeyIsSet pins the other branch of
// gateway selection: with no CLI but a key in the environment, calls are
// served over the direct API, and doctor must say so.
func TestRunDoctor_ReportsTheAPIGatewayWhenAKeyIsSet(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-not-a-real-key")

	rep, err := whetstone.RunDoctor(context.Background(), false)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if rep.Gateway != "api" {
		t.Errorf("gateway = %q, want %q when an API key is set and no CLI is on PATH", rep.Gateway, "api")
	}
}

// TestRunDoctor_ListsEveryRegisteredAdapter is the guard for the blank-import
// hazard: the adapters register only from init(), so a missing import in the
// CLI's own package silently drops an agent from every panel with no compile
// error and no panic.
func TestRunDoctor_ListsEveryRegisteredAdapter(t *testing.T) {
	t.Setenv("PATH", "")

	want := []string{"claude-code", "codex", "cursor", "opencode"}

	if got := len(agent.All()); got != len(want) {
		t.Errorf("registered adapters = %d, want %d: %v", got, len(want), adapterNames())
	}
	for _, name := range want {
		if _, ok := agent.Get(name); !ok {
			t.Errorf("adapter %q is not registered; check the blank imports in cli/whetstone", name)
		}
	}

	rep, err := whetstone.RunDoctor(context.Background(), false)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if len(rep.Adapters) != len(want) {
		t.Errorf("doctor reported %v, want %v", rep.Adapters, want)
	}
}

func adapterNames() []string {
	all := agent.All()
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = a.Name()
	}
	return out
}
