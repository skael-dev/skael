package skill_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// postReview posts a review decision for a version and returns the recorder.
// hold_reason is omitted, exercising the pre-ownership single-outstanding
// default path.
func postReview(t *testing.T, handler http.Handler, caller **auth.User, user *auth.User, name string, version int, action, reason string) *httptest.ResponseRecorder {
	t.Helper()
	return postReviewFull(t, handler, caller, user, name, version, action, reason, "")
}

// postReviewFull is postReview plus an explicit hold_reason, for tests that
// need to name which outstanding reason the decision clears.
func postReviewFull(t *testing.T, handler http.Handler, caller **auth.User, user *auth.User, name string, version int, action, reason, holdReason string) *httptest.ResponseRecorder {
	t.Helper()
	*caller = user
	var out skill.Version
	return doJSON(t, handler,
		http.MethodPost,
		fmt.Sprintf("/api/skills/%s/versions/%d/review", name, version),
		map[string]string{"action": action, "reason": reason, "hold_reason": holdReason},
		&out,
	)
}

// namespaceOwner is an OwnerResolver whose IsOwner answer depends on which
// user is asking, unlike fakeOwners's constant state — review's per-reason
// authorization resolves ownership for the *reviewing* user, who is a
// different caller from the one who originally published.
type namespaceOwner struct {
	ownerEmail string
}

func (n *namespaceOwner) ResolveForPublish(_ context.Context, _ string, user *auth.User) (gate.OwnerState, error) {
	isOwner := user != nil && user.Email == n.ownerEmail
	return gate.OwnerState{Evaluated: true, IsOwner: isOwner, RulePattern: "payments:*"}, nil
}

func (n *namespaceOwner) ClaimOnFirstPublish(_ context.Context, _ string, _ *auth.User) error {
	return nil
}

// setupReviewTestAPI is setupTestAPIAsUserWithStore, plus an ownership
// resolver, for review tests that need a namespace owner distinct from an
// instance admin.
func setupReviewTestAPI(t *testing.T, caller **auth.User, owners skill.OwnerResolver) (http.Handler, *skill.Store) {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)
	seedCanonicalTestUsers(t, pool)
	seedUser(t, pool, "00000000-0000-0000-0000-000000000010", "alice@acme.com", auth.RoleMember)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if *caller != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), *caller))
			}
			next.ServeHTTP(w, req)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{Ownership: owners})

	return r, store
}

// createHeldVersion builds a skill and a version already held for the given
// reasons, writing directly through the store rather than the publish
// pipeline — the same pattern approvals_test.go uses — so the fixture is not
// coupled to what the scanner or the ownership gate happen to trip today.
func createHeldVersion(t *testing.T, store *skill.Store, name string, reasons []string) skill.Version {
	t.Helper()
	ctx := context.Background()

	sk, err := store.Create(ctx, name, name, "", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	d := gate.Decision{Outcome: gate.NeedsReview, HoldReasons: reasons}
	v, err := store.CreateVersion(ctx, sk.ID, "a.tar.gz", "sum", "", "d", "b",
		json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "carol@acme.com", d)
	require.NoError(t, err)
	return *v
}

// A namespace owner who is a plain member may clear the ownership reason.
func TestOwnerMayClearOwnershipReason(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	alice := &auth.User{ID: "00000000-0000-0000-0000-000000000010", Email: "alice@acme.com", Role: auth.RoleMember}
	owners := &namespaceOwner{ownerEmail: "alice@acme.com"}
	handler, store := setupReviewTestAPI(t, &caller, owners)

	held := createHeldVersion(t, store, "payments:owner-clear", []string{gate.ReasonOwnership})

	resp := postReviewFull(t, handler, &caller, alice, "payments:owner-clear", held.Version,
		"approve", "confirmed this is the payments team's skill", gate.ReasonOwnership)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	v, err := store.GetVersion(t.Context(), "payments:owner-clear", held.Version)
	require.NoError(t, err)
	assert.Equal(t, "released", v.GateState)
}

// ...and may NOT clear a scan finding. Ownership is a team decision; a scan
// finding is an instance decision, and neither launders the other.
func TestOwnerMayNotClearScanReason(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	alice := &auth.User{ID: "00000000-0000-0000-0000-000000000010", Email: "alice@acme.com", Role: auth.RoleMember}
	owners := &namespaceOwner{ownerEmail: "alice@acme.com"}
	handler, store := setupReviewTestAPI(t, &caller, owners)

	held := createHeldVersion(t, store, "payments:owner-no-scan", []string{gate.ReasonScan, gate.ReasonOwnership})

	resp := postReviewFull(t, handler, &caller, alice, "payments:owner-no-scan", held.Version,
		"approve", "trust me, it's fine", gate.ReasonScan)
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

// An admin may clear either.
func TestAdminMayClearEitherReason(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	root := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "root@example.com", Role: auth.RoleOwner}
	owners := &namespaceOwner{ownerEmail: "alice@acme.com"}
	handler, store := setupReviewTestAPI(t, &caller, owners)

	held := createHeldVersion(t, store, "payments:admin-clear", []string{gate.ReasonScan, gate.ReasonOwnership})

	resp := postReviewFull(t, handler, &caller, root, "payments:admin-clear", held.Version,
		"approve", "reviewed the finding, it's a false positive", gate.ReasonScan)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	resp = postReviewFull(t, handler, &caller, root, "payments:admin-clear", held.Version,
		"approve", "confirmed ownership out of band", gate.ReasonOwnership)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	v, err := store.GetVersion(t.Context(), "payments:admin-clear", held.Version)
	require.NoError(t, err)
	assert.Equal(t, "released", v.GateState)
}

// Omitting hold_reason with two outstanding must fail loudly rather than
// guessing which one the caller meant.
func TestAmbiguousReasonIsRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	root := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "root@example.com", Role: auth.RoleOwner}
	owners := &namespaceOwner{ownerEmail: "alice@acme.com"}
	handler, store := setupReviewTestAPI(t, &caller, owners)

	held := createHeldVersion(t, store, "payments:ambiguous", []string{gate.ReasonScan, gate.ReasonOwnership})

	resp := postReviewFull(t, handler, &caller, root, "payments:ambiguous", held.Version,
		"approve", "which one?", "")
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
	assert.Contains(t, resp.Body.String(), gate.ReasonScan)
	assert.Contains(t, resp.Body.String(), gate.ReasonOwnership)
}

// Omitting it with exactly one outstanding is the pre-ownership behaviour and
// must keep working — every deployed `skael review --approve` depends on it.
func TestOmittedReasonDefaultsWhenUnambiguous(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, store := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	held := heldVersion(t, handler, &caller, "omitted-reason-ok")

	resp := postReview(t, handler, &caller, owner, "omitted-reason-ok", held.Version,
		"approve", "single outstanding reason, no ambiguity")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	v, err := store.GetVersion(t.Context(), "omitted-reason-ok", held.Version)
	require.NoError(t, err)
	assert.Equal(t, "released", v.GateState)
}

// publishCleanVersion creates a skill and publishes a clean version, released
// on arrival, and returns it.
func publishCleanVersion(t *testing.T, handler http.Handler, caller **auth.User, name string) skill.Version {
	t.Helper()
	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}
	*caller = member
	createSkill(t, handler, name, "a clean fixture")
	var body publishGateBody
	rr := postArchive(t, handler, "/api/skills/"+name+"/versions", cleanBundle(t, name))
	require.Equal(t, http.StatusCreated, rr.Code, "publish %s: %s", name, rr.Body.String())
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body.Version
}

func TestReviewApproveReleases(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, store := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	held := heldVersion(t, handler, &caller, "approve-me")

	resp := postReview(t, handler, &caller, owner, "approve-me", held.Version, "approve", "reviewed by hand, it only writes to ./out")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	sk, err := store.GetByName(t.Context(), "approve-me")
	require.NoError(t, err)
	assert.Equal(t, held.Version, sk.LatestVersion)

	v, err := store.GetVersion(t.Context(), "approve-me", held.Version)
	require.NoError(t, err)
	assert.Equal(t, "released", v.GateState)
	assert.Contains(t, v.GateNote, "only writes to ./out")
	assert.NotEmpty(t, v.GatedBy)
}

func TestReviewRejectIsTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, _ := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	held := heldVersion(t, handler, &caller, "reject-me")

	require.Equal(t, http.StatusOK, postReview(t, handler, &caller, owner, "reject-me", held.Version, "reject", "obfuscated installer").Code)
	assert.Equal(t, http.StatusConflict, postReview(t, handler, &caller, owner, "reject-me", held.Version, "approve", "changed my mind").Code)
}

func TestReviewRequiresPrivilege(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, store := setupTestAPIAsUserWithStore(t, &caller)
	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}

	held := heldVersion(t, handler, &caller, "members-cannot")

	resp := postReview(t, handler, &caller, member, "members-cannot", held.Version, "approve", "looks fine to me")
	assert.Equal(t, http.StatusForbidden, resp.Code)

	sk, err := store.GetByName(t.Context(), "members-cannot")
	require.NoError(t, err)
	assert.Equal(t, 0, sk.LatestVersion)
}

func TestReviewRequiresAReason(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, _ := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	held := heldVersion(t, handler, &caller, "needs-a-reason")
	for _, reason := range []string{"", "   "} {
		resp := postReview(t, handler, &caller, owner, "needs-a-reason", held.Version, "approve", reason)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.Code,
			"an override with no written justification is the one that gets forgotten")
	}
}

func TestReviewRejectsAnUnknownAction(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, _ := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	held := heldVersion(t, handler, &caller, "bad-action")
	assert.Equal(t, http.StatusUnprocessableEntity,
		postReview(t, handler, &caller, owner, "bad-action", held.Version, "maybe", "hmm").Code)
}

func TestReviewOnAReleasedVersionIsAConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, _ := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	v := publishCleanVersion(t, handler, &caller, "already-released")
	assert.Equal(t, http.StatusConflict,
		postReview(t, handler, &caller, owner, "already-released", v.Version, "approve", "redundant").Code)
}

func TestReviewOnAMissingVersionIs404(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, _ := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	caller = owner
	createSkill(t, handler, "no-such-version", "empty")
	assert.Equal(t, http.StatusNotFound,
		postReview(t, handler, &caller, owner, "no-such-version", 7, "approve", "reason").Code)
}

// heldVersion publishes an appealable bundle and returns the held version it
// created, read directly from the API so the test does not depend on the
// publish response body shape.
func heldVersion(t *testing.T, handler http.Handler, caller **auth.User, name string) skill.Version {
	t.Helper()
	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}
	*caller = member
	createSkill(t, handler, name, "a held fixture")
	rr := postArchive(t, handler, "/api/skills/"+name+"/versions", appealableBundle(t, name))
	require.Equal(t, http.StatusCreated, rr.Code, "publish %s: %s", name, rr.Body.String())

	var body publishGateBody
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, "needs_review", body.GateState, "fixture must actually be held")
	return body.Version
}
