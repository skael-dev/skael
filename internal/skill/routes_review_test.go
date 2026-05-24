package skill_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/skill"
)

// -----------------------------------------------------------------
// PUT /api/skills/{name}/review
// -----------------------------------------------------------------

func TestReviewSkill_200(t *testing.T) {
	handler, store, _ := setupTestAPI(t)

	createSkill(t, handler, "reviewed-skill", "will be reviewed")

	// Mark as reviewed.
	var resp skill.Skill
	rr := doJSON(t, handler, http.MethodPut, "/api/skills/reviewed-skill/review", nil, &resp)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, "reviewed-skill", resp.Name)
	require.NotNil(t, resp.ReviewedAt, "reviewed_at should be set after review")

	// Confirm via GET that reviewed_at is persisted.
	sk, err := store.GetByName(context.Background(), "reviewed-skill")
	require.NoError(t, err)
	require.NotNil(t, sk)
	require.NotNil(t, sk.ReviewedAt, "GET should show reviewed_at as non-null")
}

func TestReviewSkill_404(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	rr := doJSON(t, handler, http.MethodPut, "/api/skills/no-such-skill/review", nil, nil)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// -----------------------------------------------------------------
// DELETE /api/skills/{name}/review
// -----------------------------------------------------------------

func TestUnreviewSkill_204(t *testing.T) {
	handler, store, _ := setupTestAPI(t)

	createSkill(t, handler, "unreviewed-skill", "will be unreviewed")

	// First mark as reviewed.
	rr := doJSON(t, handler, http.MethodPut, "/api/skills/unreviewed-skill/review", nil, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Confirm it is reviewed.
	sk, err := store.GetByName(context.Background(), "unreviewed-skill")
	require.NoError(t, err)
	require.NotNil(t, sk.ReviewedAt)

	// Now clear the review.
	req := httptest.NewRequest(http.MethodDelete, "/api/skills/unreviewed-skill/review", nil)
	rrDel := httptest.NewRecorder()
	handler.ServeHTTP(rrDel, req)
	require.Equal(t, http.StatusNoContent, rrDel.Code, rrDel.Body.String())

	// Confirm via store that reviewed_at is nil again.
	sk, err = store.GetByName(context.Background(), "unreviewed-skill")
	require.NoError(t, err)
	require.NotNil(t, sk)
	require.Nil(t, sk.ReviewedAt, "reviewed_at should be null after unreview")
}

func TestUnreviewSkill_404(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/skills/no-such-skill/review", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// -----------------------------------------------------------------
// PUT /api/skills/review — bulk review
// -----------------------------------------------------------------

func TestBulkReviewSkills(t *testing.T) {
	handler, store, _ := setupTestAPI(t)

	createSkill(t, handler, "bulk-a", "first")
	createSkill(t, handler, "bulk-b", "second")

	var resp struct {
		Reviewed int `json:"reviewed"`
	}
	rr := doJSON(t, handler, http.MethodPut, "/api/skills/review",
		map[string]interface{}{"names": []string{"bulk-a", "bulk-b"}}, &resp)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, 2, resp.Reviewed)

	// Confirm both are reviewed in the DB.
	for _, name := range []string{"bulk-a", "bulk-b"} {
		sk, err := store.GetByName(context.Background(), name)
		require.NoError(t, err)
		require.NotNil(t, sk, "skill %q should exist", name)
		require.NotNil(t, sk.ReviewedAt, "skill %q should be reviewed", name)
	}
}

// TestBulkReview_RouteNotConflicting verifies that the static /api/skills/review
// path is not captured by the parameterized /api/skills/{name} routes.
func TestBulkReview_RouteNotConflicting(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "route-check", "routing test")

	// A PUT to /api/skills/review should hit the bulk endpoint, not treat
	// "review" as a skill name.
	var resp struct {
		Reviewed int `json:"reviewed"`
	}
	rr := doJSON(t, handler, http.MethodPut, "/api/skills/review",
		map[string]interface{}{"names": []string{"route-check"}}, &resp)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

// -----------------------------------------------------------------
// Publishing a new version resets reviewed_at to null (Task 1 CreateVersion change)
// -----------------------------------------------------------------

func TestPublishVersion_ResetsReviewedAt(t *testing.T) {
	handler, store, _ := setupTestAPI(t)

	createSkill(t, handler, "reset-review-skill", "will be reviewed then published")

	// Mark as reviewed.
	rr := doJSON(t, handler, http.MethodPut, "/api/skills/reset-review-skill/review", nil, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Confirm it is reviewed.
	sk, err := store.GetByName(context.Background(), "reset-review-skill")
	require.NoError(t, err)
	require.NotNil(t, sk.ReviewedAt, "should be reviewed before publish")

	// Publish a new version — this should reset reviewed_at to null.
	archiveBytes := buildTestArchive(t, "reset-review-skill", "updated")
	publishVersion(t, handler, "reset-review-skill", archiveBytes)

	// Confirm reviewed_at is null again.
	sk, err = store.GetByName(context.Background(), "reset-review-skill")
	require.NoError(t, err)
	require.NotNil(t, sk)
	require.Nil(t, sk.ReviewedAt, "publishing a new version should reset reviewed_at to null")
}
