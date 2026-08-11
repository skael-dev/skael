package whetstone_test

import (
	"testing"

	_ "github.com/skael-dev/skael/cli/whetstone" // link the blank import
	"github.com/skael-dev/skael/internal/eval/agent"
)

func TestAdapterRegistry_IsCompleteAtTheCLIEntrypoint(t *testing.T) {
	all := agent.All()
	// The adapter registers only from a blank-import init(). A forgotten import
	// makes agent.Get return (nil, false) with no compile error and no panic —
	// an empty panel on every score. Asserting the count at the entrypoint is
	// the only thing that catches it.
	if len(all) != 1 {
		t.Fatalf("%d adapters registered, want 1: %v", len(all), names(all))
	}
	if _, ok := agent.Get("claude-code"); !ok {
		t.Error("adapter \"claude-code\" does not resolve; its blank import is missing from cli/whetstone")
	}
}

func names(as []agent.Adapter) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name()
	}
	return out
}
