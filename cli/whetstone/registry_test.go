package whetstone_test

import (
	"testing"

	_ "github.com/skael-dev/skael/cli/whetstone" // link the blank imports
	"github.com/skael-dev/skael/internal/eval/agent"
)

func TestAdapterRegistry_IsCompleteAtTheCLIEntrypoint(t *testing.T) {
	all := agent.All()
	// Adapters register only from a blank-import init(). A forgotten import
	// makes agent.Get return (nil, false) with no compile error and no panic —
	// a panel member silently absent from every score. Asserting the count at
	// the entrypoint is the only thing that catches it.
	if len(all) != 4 {
		t.Fatalf("%d adapters registered, want 4: %v", len(all), names(all))
	}
	for _, want := range []string{"claude-code", "codex", "cursor", "opencode"} {
		if _, ok := agent.Get(want); !ok {
			t.Errorf("adapter %q does not resolve; its blank import is missing from cli/whetstone", want)
		}
	}
}

func names(as []agent.Adapter) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name()
	}
	return out
}
