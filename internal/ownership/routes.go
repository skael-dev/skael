package ownership

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
)

// actorFrom builds the Actor CanManage needs from the authenticated user on
// the request context. A nil user (no session attached) is never privileged
// and never a member of anything, so it can manage nothing.
func actorFrom(user *auth.User) Actor {
	if user == nil {
		return Actor{}
	}
	return Actor{UserID: user.ID, Privileged: user.IsPrivileged()}
}

// validateMembers resolves every id in memberIDs through users, returning a
// 422 naming the first id that does not resolve to a real account. An
// unknown id is never silently dropped — a rule that looks like it has
// members but does not is worse than one that fails to save.
func validateMembers(ctx context.Context, users UserLookup, memberIDs []string) error {
	for _, id := range memberIDs {
		row, err := users.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("ownership: look up member %q: %w", id, err)
		}
		if row == nil {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("unknown user id %q", id))
		}
	}
	return nil
}

// ruleBody is the wire shape of a rule: id, pattern, and hydrated members.
// Members carries {id, name, email} rather than bare ids so a client can
// render the rule without a second round trip per member — the same
// hydration GET /api/skills/{name}/owners already does via ownerRefBody.
type ruleBody struct {
	ID      string         `json:"id"`
	Pattern string         `json:"pattern"`
	Members []ownerRefBody `json:"members"`
}

// hydrateMembers resolves each member id in r through users, in order,
// skipping any id whose account has gone rather than failing the whole list
// — the same tolerance Resolver.ResolveForPublish gives a deleted member.
func hydrateMembers(ctx context.Context, users UserLookup, r *Rule) []ownerRefBody {
	members := make([]ownerRefBody, 0, len(r.Members))
	for _, id := range r.Members {
		row, err := users.GetByID(ctx, id)
		if err != nil || row == nil {
			continue
		}
		members = append(members, ownerRefBody{ID: row.ID, Name: row.Name, Email: row.Email})
	}
	return members
}

func toRuleBody(ctx context.Context, users UserLookup, r *Rule) ruleBody {
	return ruleBody{ID: r.ID, Pattern: r.Pattern, Members: hydrateMembers(ctx, users, r)}
}

// findByID returns the rule with id from rules, or nil if none matches.
func findByID(rules []Rule, id string) *Rule {
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

// RegisterRoutes wires up the ownership rule CRUD API (§9.1) and the
// resolved-owners lookup for a skill name. Every mutating handler loads the
// current rule set, builds an Actor from the authenticated user, and calls
// CanManage before writing anything — this is the entire escalation surface
// of the ownership feature.
func RegisterRoutes(api huma.API, store *Store, users UserLookup) {
	// -----------------------------------------------------------------
	// GET /api/ownership/rules — list every rule and its members. Open to
	// any authenticated user: seeing who owns what is not itself a
	// privileged operation, only changing it is.
	// -----------------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID: "list-ownership-rules",
		Method:      http.MethodGet,
		Path:        "/api/ownership/rules",
		Summary:     "List every ownership rule and its members",
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Rules []ruleBody `json:"rules"`
		}
	}, error) {
		rules, err := store.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("ownership: list rules: %w", err)
		}
		out := &struct {
			Body struct {
				Rules []ruleBody `json:"rules"`
			}
		}{}
		out.Body.Rules = make([]ruleBody, len(rules))
		for i := range rules {
			out.Body.Rules[i] = toRuleBody(ctx, users, &rules[i])
		}
		return out, nil
	})

	// -----------------------------------------------------------------
	// POST /api/ownership/rules — create a rule, or replace the member list
	// of one that already exists at that pattern (Upsert's contract).
	// -----------------------------------------------------------------
	type createRuleBody struct {
		Pattern string   `json:"pattern" minLength:"1"`
		Members []string `json:"members"`
	}
	type createRuleInput struct {
		Body createRuleBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "create-ownership-rule",
		Method:        http.MethodPost,
		Path:          "/api/ownership/rules",
		Summary:       "Create an ownership rule, or replace an existing one's members",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *createRuleInput) (*struct{ Body ruleBody }, error) {
		user := auth.UserFromContext(ctx)
		pattern := input.Body.Pattern

		if err := ValidatePattern(pattern); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}

		rules, err := store.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("ownership: list rules: %w", err)
		}

		if !CanManage(actorFrom(user), pattern, rules) {
			return nil, huma.Error403Forbidden(
				fmt.Sprintf("you may not manage pattern %q", pattern))
		}

		if len(input.Body.Members) == 0 {
			return nil, huma.Error422UnprocessableEntity(ErrNoMembers.Error())
		}
		if err := validateMembers(ctx, users, input.Body.Members); err != nil {
			return nil, err
		}

		createdBy := ""
		if user != nil {
			createdBy = user.ID
		}
		r, err := store.Upsert(ctx, pattern, input.Body.Members, createdBy)
		if err != nil {
			if errors.Is(err, ErrNoMembers) {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return nil, fmt.Errorf("ownership: create rule: %w", err)
		}
		return &struct{ Body ruleBody }{Body: toRuleBody(ctx, users, r)}, nil
	})

	// -----------------------------------------------------------------
	// PUT /api/ownership/rules/{id} — replace the members of an existing
	// rule. The pattern is not editable here: rules are addressed by id
	// precisely so a pattern can never need escaping in a path segment, and
	// changing a pattern out from under an id would be the same footgun.
	// -----------------------------------------------------------------
	type updateRuleBody struct {
		Members []string `json:"members"`
	}
	type updateRuleInput struct {
		ID   string `path:"id"`
		Body updateRuleBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "update-ownership-rule",
		Method:        http.MethodPut,
		Path:          "/api/ownership/rules/{id}",
		Summary:       "Replace the members of an ownership rule",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *updateRuleInput) (*struct{ Body ruleBody }, error) {
		user := auth.UserFromContext(ctx)

		rules, err := store.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("ownership: list rules: %w", err)
		}

		existing := findByID(rules, input.ID)
		if existing == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("ownership rule %q not found", input.ID))
		}

		if !CanManage(actorFrom(user), existing.Pattern, rules) {
			return nil, huma.Error403Forbidden(
				fmt.Sprintf("you may not manage pattern %q", existing.Pattern))
		}

		if len(input.Body.Members) == 0 {
			return nil, huma.Error422UnprocessableEntity(ErrNoMembers.Error())
		}
		if err := validateMembers(ctx, users, input.Body.Members); err != nil {
			return nil, err
		}

		updatedBy := ""
		if user != nil {
			updatedBy = user.ID
		}
		r, err := store.Upsert(ctx, existing.Pattern, input.Body.Members, updatedBy)
		if err != nil {
			if errors.Is(err, ErrNoMembers) {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return nil, fmt.Errorf("ownership: update rule: %w", err)
		}
		return &struct{ Body ruleBody }{Body: toRuleBody(ctx, users, r)}, nil
	})

	// -----------------------------------------------------------------
	// DELETE /api/ownership/rules/{id}
	// -----------------------------------------------------------------
	type deleteRuleInput struct {
		ID string `path:"id"`
	}
	huma.Register(api, huma.Operation{
		OperationID:   "delete-ownership-rule",
		Method:        http.MethodDelete,
		Path:          "/api/ownership/rules/{id}",
		Summary:       "Delete an ownership rule",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *deleteRuleInput) (*struct {
		Body struct {
			Deleted bool `json:"deleted"`
		}
	}, error) {
		user := auth.UserFromContext(ctx)

		rules, err := store.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("ownership: list rules: %w", err)
		}

		existing := findByID(rules, input.ID)
		if existing == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("ownership rule %q not found", input.ID))
		}

		if !CanManage(actorFrom(user), existing.Pattern, rules) {
			return nil, huma.Error403Forbidden(
				fmt.Sprintf("you may not manage pattern %q", existing.Pattern))
		}

		deleted, err := store.Delete(ctx, input.ID)
		if err != nil {
			return nil, fmt.Errorf("ownership: delete rule: %w", err)
		}
		out := &struct {
			Body struct {
				Deleted bool `json:"deleted"`
			}
		}{}
		out.Body.Deleted = deleted
		return out, nil
	})

	// -----------------------------------------------------------------
	// GET /api/skills/{name}/owners — the resolved owners for a skill name
	// and which rule produced them, open to any authenticated user so it is
	// never a mystery why someone does or does not have access. Built on
	// the same Resolver publish uses, so this and a publish decision can
	// never disagree about who owns a name.
	// -----------------------------------------------------------------
	resolver := NewResolver(store, users)
	type ownersInput struct {
		Name string `path:"name"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-owners",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/owners",
		Summary:     "Resolved owners for a skill name and the rule that matched",
	}, func(ctx context.Context, input *ownersInput) (*struct {
		Body struct {
			RulePattern string         `json:"rule_pattern,omitempty"`
			Owners      []ownerRefBody `json:"owners"`
			Unowned     bool           `json:"unowned"`
		}
	}, error) {
		// A nil user skips only IsOwner, which this endpoint does not
		// report — RulePattern, Owners, and Unowned are all computed
		// regardless of who is asking.
		st, err := resolver.ResolveForPublish(ctx, input.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("ownership: resolve owners: %w", err)
		}
		out := &struct {
			Body struct {
				RulePattern string         `json:"rule_pattern,omitempty"`
				Owners      []ownerRefBody `json:"owners"`
				Unowned     bool           `json:"unowned"`
			}
		}{}
		out.Body.RulePattern = st.RulePattern
		out.Body.Unowned = st.Unowned
		out.Body.Owners = make([]ownerRefBody, len(st.Owners))
		for i, o := range st.Owners {
			out.Body.Owners[i] = ownerRefBody{ID: o.ID, Name: o.Name, Email: o.Email}
		}
		return out, nil
	})
}

// ownerRefBody mirrors gate.OwnerRef's wire shape. Declared separately
// rather than reused directly to keep this package's public response shapes
// independent of gate's — the same discipline import's importQualityState
// uses to avoid depending on a route-local type in another package.
type ownerRefBody struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
