package skill_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
)

// TestPublish_UnchangedRepublish_ApprovedAfterHeld is the critical regression
// case: a version held on first publish, approved by an admin (GateState
// advances to "released", skills.latest_version now points at it), then
// republished byte-identical. The unchanged-checksum branch must report the
// version's persisted state, not recompute a fresh decision with no quality
// evidence in play — that recompute would say needs_review again for a
// version that is actually live and being served.
func TestPublish_UnchangedRepublish_ApprovedAfterHeld(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, _ := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	held := heldVersion(t, handler, &caller, "approved-then-republished")
	require.Equal(t, http.StatusOK,
		postReview(t, handler, &caller, owner, "approved-then-republished", held.Version, "approve", "checked by hand, only writes to ./out").Code)

	// Republish the exact same bytes as a member (any caller — this is a
	// no-op republish, not a privileged action).
	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}
	caller = member
	rr := postArchive(t, handler, "/api/skills/approved-then-republished/versions", appealableBundle(t, "approved-then-republished"))
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	var body publishGateBody
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))

	assert.False(t, body.Created, "byte-identical content must hit the unchanged-checksum short-circuit")
	assert.Equal(t, "released", body.GateState,
		"the version was approved; it must still read released after an unchanged republish")
	assert.Equal(t, held.Version, body.Version.Version)
}

// TestPublish_UnchangedRepublish_StillHeld would cover a version that
// genuinely remains needs_review at the time of an unchanged republish.
//
// Investigation finding: this case is unreachable through the publish route.
// skills.latest_version only ever advances for a released version
// (skill.Store.CreateVersion sets it only when `released`; ReleaseVersion is
// the only other writer). The unchanged-checksum branch in routes.go compares
// the incoming checksum against sk.LatestVersion's row specifically — so by
// the time that branch can ever fire, the version it loads is already
// "released" by construction. A version still sitting in needs_review is
// never pointed at by latest_version, so no republish of its bytes can ever
// match it in that comparison; a republish of the same held content instead
// falls through to the normal publish path and mints a brand new version,
// which is scored/held independently rather than hitting the unchanged
// short-circuit at all.
//
// No test is written for this sub-case because there is no way to construct
// it: it is not a gap in coverage, it is a state the code cannot reach.
func TestPublish_UnchangedRepublish_StillHeld_Unreachable(t *testing.T) {
	t.Skip("documented as unreachable: see comment above — skills.latest_version never points at a needs_review version")
}

// TestPublish_UnchangedRepublish_Rejected: same investigation, for
// "rejected". skill.Store.RejectVersion never writes to
// skills.latest_version (only ReleaseVersion and the released branch of
// CreateVersion do), so a rejected version can never be sk.LatestVersion
// either, and the unchanged-checksum branch can never load one. Confirmed by
// reading gate_store.go's RejectVersion, which updates only the version row.
func TestPublish_UnchangedRepublish_Rejected_Unreachable(t *testing.T) {
	t.Skip("documented as unreachable: RejectVersion never advances skills.latest_version, so a rejected version is never the unchanged-checksum branch's `latest`")
}

// TestPublish_UnchangedRepublish_DecisionSurvivesRecomputeDivergence is the
// test that can actually distinguish "persisted snapshot" from "fresh
// recompute" at the Decision.Outcome level, not just via GateState.
//
// DecidePublish (see routes.go) always passes nil quality evidence, so a
// recompute of the same scan report as a review-approved version's original
// publish is deterministically identical to what was first decided — a
// review approval alone can never make the two diverge, which is why
// TestPublish_UnchangedRepublish_ApprovedAfterHeld's Decision.Outcome would
// stay "needs_review" either way and cannot catch a regression back to
// recomputing.
//
// An admin override, though, is exactly such a divergence: the *first*
// publish is decided with AdminOverride=true (Outcome=Allow, released
// immediately, no review needed), but a later unchanged republish by a
// non-privileged, non-override caller recomputes with AdminOverride=false —
// which would say needs_review for a version that has been served since the
// moment it was created. This is what actually exercises the mutation:
// reverting the fix (always using the fresh recompute) flips this test's
// Decision.Outcome from "allow" to "needs_review" while GateState correctly
// stays "released" throughout.
func TestPublish_UnchangedRepublish_DecisionSurvivesRecomputeDivergence(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler := setupTestAPIAsUser(t, &caller)
	admin := &auth.User{ID: "00000000-0000-0000-0000-000000000002", Email: "admin@example.com", Role: auth.RoleAdmin}

	createSkill(t, handler, "override-then-republished", "appealable")
	caller = admin
	first := postArchive(t, handler, "/api/skills/override-then-republished/versions?override=true", appealableBundle(t, "override-then-republished"))
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	var firstBody publishGateBody
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstBody))
	require.Equal(t, "released", firstBody.GateState, "an accepted override must release the version immediately")
	require.Equal(t, gate.Allow, firstBody.Decision.Outcome)

	// Republish the identical bytes as a non-privileged, non-override caller.
	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}
	caller = member
	second := postArchive(t, handler, "/api/skills/override-then-republished/versions", appealableBundle(t, "override-then-republished"))
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())

	var secondBody publishGateBody
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondBody))

	assert.False(t, secondBody.Created, "byte-identical content must hit the unchanged-checksum short-circuit")
	assert.Equal(t, "released", secondBody.GateState, "still released — nothing about a re-upload changes what was already served")
	assert.Equal(t, gate.Allow, secondBody.Decision.Outcome,
		"the persisted decision from the original override must survive; a fresh recompute (no override this time) would wrongly say needs_review")
}

// TestPublish_UnchangedRepublish_PersistsOriginalDecisionSnapshot guards the
// other half of the fix: the Decision returned alongside a released
// unchanged republish is the original snapshot from when the version was
// first decided (still needs_review, since decisions are not rewritten), not
// a bare zero-value. Callers must not mistake the historical Decision for a
// live signal — GateState is what settles that, which the previous test
// covers; this one only confirms the persisted snapshot survives the
// unmarshal round trip untouched.
func TestPublish_UnchangedRepublish_PersistsOriginalDecisionSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	var caller *auth.User
	handler, _ := setupTestAPIAsUserWithStore(t, &caller)
	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}

	held := heldVersion(t, handler, &caller, "decision-snapshot")
	require.Equal(t, http.StatusOK,
		postReview(t, handler, &caller, owner, "decision-snapshot", held.Version, "approve", "checked by hand, only writes to ./out").Code)

	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}
	caller = member
	rr := postArchive(t, handler, "/api/skills/decision-snapshot/versions", appealableBundle(t, "decision-snapshot"))
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	var body publishGateBody
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))

	require.NotEmpty(t, body.Decision.Reasons, "the original appealable finding must still be in the persisted snapshot")
	assert.Equal(t, gate.NeedsReview, body.Decision.Outcome,
		"the decision is a historical snapshot from when the version was first decided, not a live recompute")
	assert.Equal(t, "released", body.GateState, "GateState, not Decision, is what says this version is actually live")
}
