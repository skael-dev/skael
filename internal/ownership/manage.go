package ownership

// Actor is the resolved identity of whoever is asking to change a rule.
// Privileged is folded in at the HTTP boundary — the same discipline
// gate.Policy.AdminOverride uses — so this package never learns about
// auth.User and stays a pure function of its inputs.
type Actor struct {
	UserID     string
	Privileged bool // instance owner or admin
}

// StrictlyContains reports whether every name in inner's scope also falls in
// outer's scope, and the two patterns differ.
//
// An exact pattern strictly contains nothing: its scope is a single name, so
// the only pattern whose scope it covers is itself, which is not strict.
func StrictlyContains(outer, inner string) bool {
	if outer == inner {
		return false
	}
	if !IsPrefix(outer) {
		return false
	}
	os := Scope(outer)
	is := Scope(inner)
	// inner's scope must start with outer's. For an exact inner, Scope is the
	// name itself, which is the correct test either way.
	if len(is) < len(os) {
		return false
	}
	return is[:len(os)] == os
}

// CanManage reports whether a may create, replace the members of, or delete
// the rule for pattern. One function covers all three: when creating, no rule
// with that pattern exists yet, so clause 2 is simply vacuous.
//
// An actor may manage pattern P if any of:
//
//  1. They are an instance owner or admin.
//  2. They are a member of P's own rule.
//  3. They are a member of a rule that strictly contains P.
//
// Clause 3 is load-bearing. Without it delegation is one-way: an owner of
// payments:* who carves payments:refunds out to someone else stops being an
// owner of refunds (longest match replaces) and could never take it back.
// It never permits widening, because no rule the delegate belongs to
// contains the enclosing namespace.
func CanManage(a Actor, pattern string, rules []Rule) bool {
	if a.Privileged {
		return true
	}
	for _, r := range rules {
		isMember := false
		for _, m := range r.Members {
			if m == a.UserID {
				isMember = true
				break
			}
		}
		if !isMember {
			continue
		}
		if r.Pattern == pattern {
			return true // clause 2
		}
		if StrictlyContains(r.Pattern, pattern) {
			return true // clause 3
		}
	}
	return false
}
