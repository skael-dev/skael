package ownership_test

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/skael-dev/skael/internal/ownership"
)

func TestStrictlyContains(t *testing.T) {
	cases := []struct {
		outer, inner string
		want         bool
	}{
		{"payments:*", "payments:refunds", true},
		{"payments:*", "payments:refunds:*", true},
		{"*", "payments:*", true},
		{"*", "anything", true},
		{"payments:*", "payments:*", false},        // not strict
		{"payments:refunds", "payments:refunds", false},
		{"payments:refunds", "payments:refunds:eu", false}, // exact contains nothing
		{"payments:*", "billing:x", false},
		{"payments:refunds", "payments:*", false},  // narrower cannot contain wider
	}
	for _, c := range cases {
		if got := ownership.StrictlyContains(c.outer, c.inner); got != c.want {
			t.Errorf("StrictlyContains(%q, %q) = %v, want %v", c.outer, c.inner, got, c.want)
		}
	}
}

func fixture() []ownership.Rule {
	return []ownership.Rule{
		{ID: "r1", Pattern: "payments:*", Members: []string{"alice", "bob"}},
		{ID: "r2", Pattern: "payments:refunds", Members: []string{"carol"}},
	}
}

func TestCanManageClauses(t *testing.T) {
	rules := fixture()
	member := func(id string) ownership.Actor { return ownership.Actor{UserID: id} }
	admin := ownership.Actor{UserID: "root", Privileged: true}

	cases := []struct {
		who     ownership.Actor
		pattern string
		want    bool
		why     string
	}{
		{admin, "anything:*", true, "clause 1 — instance admin"},
		{member("carol"), "payments:refunds", true, "clause 2 — member of the rule itself"},
		{member("alice"), "payments:refunds", true, "clause 3 — reclaim from inside an owned namespace"},
		{member("alice"), "payments:disputes", true, "clause 3 — create a new rule inside an owned namespace"},
		{member("carol"), "payments:*", false, "no widening"},
		{member("carol"), "billing:*", false, "unrelated namespace"},
		{member("carol"), "payments:refunds:eu:*", false, "an exact rule strictly contains nothing"},
		{member("nobody"), "payments:*", false, "not a member of anything"},
		{member("alice"), "*", false, "cannot reach the root from a namespace"},
	}
	for _, c := range cases {
		if got := ownership.CanManage(c.who, c.pattern, rules); got != c.want {
			t.Errorf("CanManage(%s, %q) = %v, want %v (%s)", c.who.UserID, c.pattern, got, c.want, c.why)
		}
	}
}

// THE test. Three individually-correct clauses can compose into a path that
// widens scope in two steps, and no per-clause unit test can see it. Generate
// random legal edit sequences by a non-admin and assert the invariant:
//
//	a non-admin never gains manage rights over any name outside the union of
//	the scopes they could manage at the start.
func TestCanManageNeverEscalatesScope(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	// Names probed after every edit. Deliberately spans inside, adjacent to,
	// and far from the actor's starting scope.
	probes := []string{
		"payments:refunds", "payments:refunds:eu", "payments:invoices",
		"payments", "payroll:tax", "billing:dunning", "root",
	}

	for trial := 0; trial < 500; trial++ {
		rules := []ownership.Rule{
			{ID: "r0", Pattern: "*", Members: []string{"root"}},
			{ID: "r1", Pattern: "payments:*", Members: []string{"mallory"}},
			{ID: "r2", Pattern: "billing:*", Members: []string{"victim"}},
		}
		actor := ownership.Actor{UserID: "mallory"}

		// Baseline: which probe names can mallory manage before any edit?
		baseline := map[string]bool{}
		for _, p := range probes {
			baseline[p] = ownership.CanManage(actor, p, rules)
		}

		for step := 0; step < 8; step++ {
			candidates := []string{
				"payments:*", "payments:refunds", "payments:refunds:eu:*",
				"billing:*", "billing:dunning", "*", "payroll:*", "root",
			}
			pat := candidates[rng.Intn(len(candidates))]
			if !ownership.CanManage(actor, pat, rules) {
				continue // illegal edit; a real handler would 403
			}

			// A legal edit: create-or-replace the rule with mallory as sole
			// member, or delete it. Both are things the API permits.
			if rng.Intn(2) == 0 {
				found := false
				for i := range rules {
					if rules[i].Pattern == pat {
						rules[i].Members = []string{"mallory"}
						found = true
					}
				}
				if !found {
					rules = append(rules, ownership.Rule{
						ID:      fmt.Sprintf("gen%d-%d", trial, step),
						Pattern: pat,
						Members: []string{"mallory"},
					})
				}
			} else {
				kept := rules[:0:0]
				for _, r := range rules {
					if r.Pattern != pat {
						kept = append(kept, r)
					}
				}
				rules = kept
			}

			for _, p := range probes {
				if ownership.CanManage(actor, p, rules) && !baseline[p] {
					t.Fatalf("trial %d step %d: escalation — mallory gained manage rights over %q "+
						"which was outside their starting scope. rules=%v", trial, step, p, rules)
				}
			}
		}
	}
}
