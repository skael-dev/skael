package ownership_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/ownership"
)

func TestValidatePattern(t *testing.T) {
	cases := []struct {
		pattern string
		ok      bool
	}{
		{"payments:refunds", true},
		{"payments:*", true},
		{"*", true},
		{"a", true},
		{"superpowers:brainstorming", true},
		{"skill.name-v2", true},
		{"", false},
		{"Payments:*", false},  // uppercase
		{"pay*ments", false},   // mid-string glob
		{"payments:**", false}, // double glob
		{"payments:?", false},  // no character classes
		{"-leading", false},    // must start alnum
		{"trailing-", false},   // must end alnum (before any *)
	}
	for _, c := range cases {
		err := ownership.ValidatePattern(c.pattern)
		if c.ok && err != nil {
			t.Errorf("ValidatePattern(%q) = %v, want nil", c.pattern, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidatePattern(%q) = nil, want error", c.pattern)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	rules := []ownership.Rule{
		{ID: "r1", Pattern: "payments:*", Members: []string{"alice", "bob"}},
		{ID: "r2", Pattern: "payments:refunds", Members: []string{"carol"}},
		{ID: "r3", Pattern: "payments:refunds:eu:*", Members: []string{"dave"}},
	}

	cases := []struct {
		name string
		rule string
		want []string
	}{
		{"payments:refunds", "r2", []string{"carol"}},         // exact beats prefix
		{"payments:invoices", "r1", []string{"alice", "bob"}}, // prefix
		{"payments:refunds:eu:vat", "r3", []string{"dave"}},   // longest prefix wins
		{"billing:dunning", "", nil},                          // unowned
	}
	for _, c := range cases {
		got := ownership.Resolve(c.name, rules)
		if c.rule == "" {
			if got.Rule != nil {
				t.Errorf("Resolve(%q).Rule = %q, want nil", c.name, got.Rule.ID)
			}
			continue
		}
		if got.Rule == nil || got.Rule.ID != c.rule {
			t.Fatalf("Resolve(%q) matched %v, want rule %q", c.name, got.Rule, c.rule)
		}
		if len(got.Members) != len(c.want) {
			t.Fatalf("Resolve(%q).Members = %v, want %v", c.name, got.Members, c.want)
		}
	}
}

// Longest match REPLACES; it does not stack. carol owning payments:refunds
// means alice and bob do not, and that is deliberate — same as CODEOWNERS.
func TestResolveReplacesRatherThanStacks(t *testing.T) {
	rules := []ownership.Rule{
		{ID: "r1", Pattern: "payments:*", Members: []string{"alice"}},
		{ID: "r2", Pattern: "payments:refunds", Members: []string{"carol"}},
	}
	got := ownership.Resolve("payments:refunds", rules)
	for _, m := range got.Members {
		if m == "alice" {
			t.Fatal("Resolve stacked the prefix rule onto the exact rule; it must replace")
		}
	}
}

// A prefix pattern must match on the full segment boundary it declares, not
// on a bare string prefix: 'pay:*' must not capture 'payroll:x'.
func TestResolvePrefixIsNotBareStringPrefix(t *testing.T) {
	rules := []ownership.Rule{{ID: "r1", Pattern: "pay:*", Members: []string{"alice"}}}
	if got := ownership.Resolve("payroll:tax", rules); got.Rule != nil {
		t.Fatalf("Resolve(payroll:tax) matched %q, want unowned", got.Rule.Pattern)
	}
	if got := ownership.Resolve("pay:tax", rules); got.Rule == nil {
		t.Fatal("Resolve(pay:tax) was unowned, want rule r1")
	}
}

func TestResolveBareStarMatchesEverything(t *testing.T) {
	rules := []ownership.Rule{{ID: "r1", Pattern: "*", Members: []string{"alice"}}}
	if got := ownership.Resolve("anything:at:all", rules); got.Rule == nil {
		t.Fatal("bare * did not match")
	}
}
