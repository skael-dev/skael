package gate_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassOf(t *testing.T) {
	cases := map[string]gate.Class{
		"exfiltration": gate.ClassExfiltration,
		"secrets":      gate.ClassSecret,
		"execution":    gate.ClassExecution,
		"injection":    gate.ClassInjection,
		"obfuscation":  gate.ClassHeuristic,
	}
	for category, want := range cases {
		got, ok := gate.ClassOf(category)
		require.True(t, ok, "category %q must map to a class", category)
		assert.Equal(t, want, got, "category %q", category)
	}
}

func TestClassOfUnknown(t *testing.T) {
	_, ok := gate.ClassOf("something-nobody-defined")
	assert.False(t, ok, "an unmapped category must report itself as unmapped, not default silently")
}

// TestEveryRuleHasAClass is the guard that makes an unmapped class unreachable
// from native rules. A new rule file with a new Category fails here rather
// than reaching Decide, where the fallback is Block.
func TestEveryRuleHasAClass(t *testing.T) {
	rules := scan.AllRules()
	require.NotEmpty(t, rules, "AllRules must not be empty; an empty registry would make this test vacuous")
	for _, r := range rules {
		// ResolvedClass, not ClassOf: a rule may override the class its
		// category implies, and the guard has to cover that path too or an
		// override typo'd to a nonsense value slips past.
		c, ok := r.ResolvedClass()
		assert.Truef(t, ok, "rule %q has category %q with no class mapping", r.Name, r.Category)
		if ok {
			_, known := gate.ClassOf(string(c))
			assert.Truef(t, known || isKnownClass(c),
				"rule %q resolves to class %q, which is not one the gate recognises", r.Name, c)
		}
	}
}

// isKnownClass reports whether c is one of the five classes Decide branches on.
func isKnownClass(c gate.Class) bool {
	switch c {
	case gate.ClassExfiltration, gate.ClassSecret, gate.ClassExecution,
		gate.ClassInjection, gate.ClassHeuristic:
		return true
	}
	return false
}
