package ownership

import (
	"context"
	"fmt"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
)

// UserLookup is the subset of auth.UserStore this package needs.
type UserLookup interface {
	GetByID(ctx context.Context, id string) (*auth.UserRow, error)
}

// Resolver adapts the store onto skill.OwnerResolver.
type Resolver struct {
	store *Store
	users UserLookup
}

// NewResolver builds a Resolver.
func NewResolver(store *Store, users UserLookup) *Resolver {
	return &Resolver{store: store, users: users}
}

// ResolveForPublish answers who owns skillName and whether user is one of
// them. Instance privilege is folded into IsOwner here, at the boundary, so
// gate.Decide stays a pure function of its inputs and the privilege check
// lives in exactly one place.
func (r *Resolver) ResolveForPublish(ctx context.Context, skillName string, user *auth.User) (gate.OwnerState, error) {
	rules, err := r.store.List(ctx)
	if err != nil {
		return gate.OwnerState{}, fmt.Errorf("ownership.Resolver.ResolveForPublish: %w", err)
	}
	res := Resolve(skillName, rules)

	st := gate.OwnerState{Evaluated: true, Unowned: res.Unowned()}
	if res.Rule != nil {
		st.RulePattern = res.Rule.Pattern
	}
	if user != nil {
		st.IsOwner = user.IsPrivileged() || res.Contains(user.ID)
	}

	// Hydrate the owner references so the CLI and the review screen can name
	// the humans who unblock a held publish.
	for _, id := range res.Members {
		row, err := r.users.GetByID(ctx, id)
		if err != nil || row == nil {
			// A member whose account has gone is not a reason to fail a
			// publish. Skip them; the remaining owners still answer "who do
			// I ask", and an emptied rule falls back to admins.
			continue
		}
		st.Owners = append(st.Owners, gate.OwnerRef{ID: row.ID, Name: row.Name, Email: row.Email})
	}
	return st, nil
}

// ClaimOnFirstPublish records user as the sole owner of skillName via an
// exact-pattern rule. The caller only invokes this when the name resolved to
// unowned, which is what keeps a covering rule winning over first publish.
func (r *Resolver) ClaimOnFirstPublish(ctx context.Context, skillName string, user *auth.User) error {
	if user == nil || user.ID == "" {
		return nil // a system publish claims nothing
	}
	if _, err := r.store.Upsert(ctx, skillName, []string{user.ID}, user.ID); err != nil {
		return fmt.Errorf("ownership.Resolver.ClaimOnFirstPublish: %w", err)
	}
	return nil
}
