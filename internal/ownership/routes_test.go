package ownership_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/testutil"
)

// newOwnershipTestServer wires the real router with ownership routes
// registered, backed by a real Postgres database and a real auth.UserStore
// so member-id validation exercises the actual lookup path.
func newOwnershipTestServer(t *testing.T) (http.Handler, *ownership.Store, *auth.UserStore, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.SetupTestDB(t)
	store := ownership.NewStore(pool)
	users := auth.NewUserStore(pool)

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	ownership.RegisterRoutes(api, store, users)
	return r, store, users, pool
}

// seedAuthUser creates a real user row (member role) so it can be referenced
// by ownership_rule_members' foreign key, and returns its row.
func seedAuthUser(t *testing.T, users *auth.UserStore, email string) *auth.UserRow {
	t.Helper()
	row, err := users.Create(context.Background(), email, email, "x")
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return row
}

func doAs(t *testing.T, handler http.Handler, method, path string, body any, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if user != nil {
		r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)
	return rr
}

// A plain member may not create a rule for a namespace nobody owns.
func TestCreateRuleRequiresAdminForANewNamespace(t *testing.T) {
	handler, _, users, _ := newOwnershipTestServer(t)
	alice := seedAuthUser(t, users, "alice@acme.com")
	member := &auth.User{ID: alice.ID, Email: alice.Email, Role: auth.RoleMember}

	rr := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "payments:*",
		"members": []string{alice.ID},
	}, member)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body)
	}
}

// An admin creates payments:* → alice. alice, a member, then manages it.
func TestNamespaceOwnerManagesTheirOwnRule(t *testing.T) {
	handler, _, users, _ := newOwnershipTestServer(t)
	adminRow := seedAuthUser(t, users, "admin@acme.com")
	alice := seedAuthUser(t, users, "alice@acme.com")
	bob := seedAuthUser(t, users, "bob@acme.com")
	admin := &auth.User{ID: adminRow.ID, Email: adminRow.Email, Role: auth.RoleAdmin}
	aliceUser := &auth.User{ID: alice.ID, Email: alice.Email, Role: auth.RoleMember}

	rr := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "payments:*",
		"members": []string{alice.ID},
	}, admin)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 200/201: %s", rr.Code, rr.Body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// alice manages her own rule by adding bob.
	rr2 := doAs(t, handler, http.MethodPut, "/api/ownership/rules/"+created.ID, map[string]any{
		"members": []string{alice.ID, bob.ID},
	}, aliceUser)
	if rr2.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200: %s", rr2.Code, rr2.Body)
	}
}

// alice may carve out a narrower rule...
func TestNamespaceOwnerMayNarrow(t *testing.T) {
	handler, _, users, _ := newOwnershipTestServer(t)
	adminRow := seedAuthUser(t, users, "admin2@acme.com")
	alice := seedAuthUser(t, users, "alice2@acme.com")
	admin := &auth.User{ID: adminRow.ID, Email: adminRow.Email, Role: auth.RoleAdmin}
	aliceUser := &auth.User{ID: alice.ID, Email: alice.Email, Role: auth.RoleMember}

	rr := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "payments:*",
		"members": []string{alice.ID},
	}, admin)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 200/201: %s", rr.Code, rr.Body)
	}

	rr2 := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "payments:refunds",
		"members": []string{alice.ID},
	}, aliceUser)
	if rr2.Code != http.StatusOK && rr2.Code != http.StatusCreated {
		t.Fatalf("narrow status = %d, want 200/201: %s", rr2.Code, rr2.Body)
	}
}

// ...and may not widen.
func TestNamespaceOwnerMayNotWiden(t *testing.T) {
	handler, _, users, _ := newOwnershipTestServer(t)
	adminRow := seedAuthUser(t, users, "admin3@acme.com")
	alice := seedAuthUser(t, users, "alice3@acme.com")
	admin := &auth.User{ID: adminRow.ID, Email: adminRow.Email, Role: auth.RoleAdmin}
	aliceUser := &auth.User{ID: alice.ID, Email: alice.Email, Role: auth.RoleMember}

	rr := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "payments:*",
		"members": []string{alice.ID},
	}, admin)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 200/201: %s", rr.Code, rr.Body)
	}

	rrStar := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "*",
		"members": []string{alice.ID},
	}, aliceUser)
	if rrStar.Code != http.StatusForbidden {
		t.Fatalf("widen to '*' status = %d, want 403: %s", rrStar.Code, rrStar.Body)
	}

	rrBilling := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "billing:*",
		"members": []string{alice.ID},
	}, aliceUser)
	if rrBilling.Code != http.StatusForbidden {
		t.Fatalf("widen to 'billing:*' status = %d, want 403: %s", rrBilling.Code, rrBilling.Body)
	}
}

// An unknown user id is a 422, never a silently-empty rule.
func TestUnknownMemberIsRejected(t *testing.T) {
	handler, _, users, _ := newOwnershipTestServer(t)
	adminRow := seedAuthUser(t, users, "admin4@acme.com")
	admin := &auth.User{ID: adminRow.ID, Email: adminRow.Email, Role: auth.RoleAdmin}

	rr := doAs(t, handler, http.MethodPost, "/api/ownership/rules", map[string]any{
		"pattern": "payments:*",
		"members": []string{"00000000-0000-0000-0000-000000000999"},
	}, admin)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rr.Code, rr.Body)
	}
}

// GET /api/skills/{name}/owners names the matching rule, so it is never a
// mystery why someone does or does not have access.
func TestSkillOwnersNamesTheMatchingRule(t *testing.T) {
	handler, store, users, _ := newOwnershipTestServer(t)
	alice := seedAuthUser(t, users, "alice5@acme.com")
	if _, err := store.Upsert(context.Background(), "payments:*", []string{alice.ID}, alice.ID); err != nil {
		t.Fatal(err)
	}

	member := &auth.User{ID: alice.ID, Email: alice.Email, Role: auth.RoleMember}
	rr := doAs(t, handler, http.MethodGet, "/api/skills/payments:refunds/owners", nil, member)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		RulePattern string `json:"rule_pattern"`
		Unowned     bool   `json:"unowned"`
		Owners      []struct {
			Email string `json:"email"`
		} `json:"owners"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RulePattern != "payments:*" {
		t.Fatalf("rule_pattern = %q, want payments:*", body.RulePattern)
	}
	if body.Unowned {
		t.Fatal("unowned = true, want false")
	}
	if len(body.Owners) != 1 || body.Owners[0].Email != "alice5@acme.com" {
		t.Fatalf("owners = %+v, want [alice5@acme.com]", body.Owners)
	}
}
