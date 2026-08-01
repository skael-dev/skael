package skill_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/skill"
)

// postReview posts a review decision for a version and returns the recorder.
func postReview(t *testing.T, handler http.Handler, caller **auth.User, user *auth.User, name string, version int, action, reason string) *httptest.ResponseRecorder {
	t.Helper()
	*caller = user
	var out skill.Version
	return doJSON(t, handler,
		http.MethodPost,
		fmt.Sprintf("/api/skills/%s/versions/%d/review", name, version),
		map[string]string{"action": action, "reason": reason},
		&out,
	)
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
