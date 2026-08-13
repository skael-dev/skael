package ownership

// Actor is the resolved identity of whoever is asking to change a rule.
type Actor struct {
	UserID     string
	Privileged bool // instance owner or admin
}

// StrictlyContains reports whether inner's scope is a proper subset of outer's.
func StrictlyContains(outer, inner string) bool {
	if outer == inner {
		return false
	}
	if !IsPrefix(outer) {
		return false
	}
	os := Scope(outer)
	is := Scope(inner)
	if len(is) < len(os) {
		return false
	}
	return is[:len(os)] == os
}

// CanManage reports whether a may create, modify, or delete the rule for
// pattern. An actor may manage P if privileged, a member of P's rule, or a
// member of a rule that strictly contains P. The third clause prevents
// one-way delegation: without it an owner of payments:* who carves out
// payments:refunds can never take it back.
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
