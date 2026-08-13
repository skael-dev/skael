package whetstone_test

import (
	"context"
	"strings"
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

	rep, err := whetstone.RunDoctor(context.Background())
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

	rep, err := whetstone.RunDoctor(context.Background())
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if rep.Gateway != "api" {
		t.Errorf("gateway = %q, want %q when an API key is set and no CLI is on PATH", rep.Gateway, "api")
	}
}

// TestRunDoctor_ListsEveryRegisteredAdapter is the guard for the blank-import
// hazard: the adapter registers only from init(), so a missing import in the
// CLI's own package silently empties every panel with no compile error and no
// panic.
func TestRunDoctor_ListsEveryRegisteredAdapter(t *testing.T) {
	t.Setenv("PATH", "")

	want := []string{"claude-code"}

	if got := len(agent.All()); got != len(want) {
		t.Errorf("registered adapters = %d, want %d: %v", got, len(want), adapterNames())
	}
	for _, name := range want {
		if _, ok := agent.Get(name); !ok {
			t.Errorf("adapter %q is not registered; check the blank imports in cli/whetstone", name)
		}
	}

	rep, err := whetstone.RunDoctor(context.Background())
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if len(rep.Adapters) != len(want) {
		t.Errorf("doctor reported %v, want %v", rep.Adapters, want)
	}
}

// TestRunDoctor_ReportsTheDefaultLLMTimeout pins the unconfigured case: no
// WHETSTONE_LLM_TIMEOUT set means the report shows authoringTimeout, not an
// empty or zero value.
func TestRunDoctor_ReportsTheDefaultLLMTimeout(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("WHETSTONE_LLM_TIMEOUT", "")

	rep, err := whetstone.RunDoctor(context.Background())
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if rep.LLMTimeout != "10m0s" {
		t.Errorf("LLMTimeout = %q, want the default authoringTimeout %q", rep.LLMTimeout, "10m0s")
	}
}

// TestRunDoctor_HonoursTheTimeoutOverride is the positive case: a valid
// WHETSTONE_LLM_TIMEOUT must be what the report shows, since that is what
// newGateway will actually apply.
func TestRunDoctor_HonoursTheTimeoutOverride(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("WHETSTONE_LLM_TIMEOUT", "45s")

	rep, err := whetstone.RunDoctor(context.Background())
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if rep.LLMTimeout != "45s" {
		t.Errorf("LLMTimeout = %q, want %q", rep.LLMTimeout, "45s")
	}
}

// TestRunDoctor_RejectsAMalformedTimeout matches the worker's own
// env-duration convention (CLAUDE.md: fail loudly, name the offending value)
// rather than the server's silent-fallback style — an interactive CLI is
// better served by catching a typo immediately than by silently reverting to
// the default.
func TestRunDoctor_RejectsAMalformedTimeout(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("WHETSTONE_LLM_TIMEOUT", "not-a-duration")

	_, err := whetstone.RunDoctor(context.Background())
	if err == nil {
		t.Fatal("RunDoctor accepted a malformed WHETSTONE_LLM_TIMEOUT")
	}
	if !strings.Contains(err.Error(), "not-a-duration") {
		t.Errorf("error does not name the offending value: %v", err)
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
