package onboarding_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/onboarding"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestSample_LoadsWithoutCompletingAStepAndRemovesEveryRow(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	skills := skill.NewStore(pool)
	s := onboarding.NewSample(pool, skills, skill.NewPublisher(skills, storage, skill.RouteOptions{}), quality.NewStore(pool), storage)

	require.NoError(t, s.Load(ctx, "owner@x.com"))
	st, err := onboarding.Status(ctx, pool, onboarding.SetupFacts{})
	require.NoError(t, err)
	require.True(t, st.SampleLoaded)
	for _, step := range st.Steps {
		require.False(t, step.Done, step.ID)
	}
	var events, scores int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM skill_events WHERE sample`).Scan(&events))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM skill_quality`).Scan(&scores))
	require.Greater(t, events, 300)
	require.Equal(t, 4, scores)
	require.Error(t, s.Load(ctx, "owner@x.com"), "a second load must refuse")

	require.NoError(t, s.Remove(ctx, "owner@x.com"))
	var left int
	require.NoError(t, pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM skills) + (SELECT count(*) FROM skill_events) + (SELECT count(*) FROM contests) +
		(SELECT count(*) FROM skill_quality) + (SELECT count(*) FROM auth_users)`).Scan(&left))
	require.Zero(t, left)
}

func TestClaim_TheCodeIsStableUntilSomeoneSignsUp(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	c := onboarding.NewClaim(pool)
	code, err := c.Ensure(ctx)
	require.NoError(t, err)
	require.Len(t, code, 14)
	again, err := c.Ensure(ctx)
	require.NoError(t, err)
	require.Equal(t, code, again)
	ok, err := c.Verify(ctx, " "+code+" ")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.Verify(ctx, "AAAA-BBBB-CCCC")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPing_SendsOnlyTheDocumentedFields(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	p, err := onboarding.BuildPing(ctx, pool, "0.19.0")
	require.NoError(t, err)
	require.NotEmpty(t, p.InstallID)
	require.NotEmpty(t, p.PostgresVersion)

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	require.NoError(t, onboarding.SendPing(ctx, srv.Client(), srv.URL, p))
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{"install_id", "version", "postgres_version", "workers", "evals_configured"}, keys)
}
