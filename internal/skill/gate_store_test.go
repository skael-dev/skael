package skill_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/sync"
	"github.com/skael-dev/skael/internal/testutil"
)

// gateFixture spins up a fresh database and a store over it.
func gateFixture(t *testing.T) (*pgxpool.Pool, *skill.Store, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	return pool, skill.NewStore(pool), context.Background()
}

func newGateSkill(t *testing.T, s *skill.Store, ctx context.Context, name string) *skill.Skill {
	t.Helper()
	sk, err := s.Create(ctx, name, name, "a skill", "content", json.RawMessage(`{}`))
	require.NoError(t, err)
	return sk
}

func newGateVersion(t *testing.T, s *skill.Store, ctx context.Context, skillID, checksum string, outcome gate.Outcome) *skill.Version {
	t.Helper()
	v, err := s.CreateVersion(ctx, skillID, "p/"+checksum, checksum, "", "d", "c",
		json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t",
		gate.Decision{Outcome: outcome, Reasons: []gate.Reason{}})
	require.NoError(t, err)
	return v
}

func TestReleaseVersionAdvancesPointer(t *testing.T) {
	_, store, ctx := gateFixture(t)
	sk := newGateSkill(t, store, ctx, "release-me")
	v := newGateVersion(t, store, ctx, sk.ID, "c1", gate.NeedsReview)

	require.NoError(t, store.ReleaseVersion(ctx, store.Pool(), "release-me", v.Version, "eval", "verified score 82"))

	got, err := store.GetByName(ctx, "release-me")
	require.NoError(t, err)
	assert.Equal(t, v.Version, got.LatestVersion)

	ver, err := store.GetVersion(ctx, "release-me", v.Version)
	require.NoError(t, err)
	assert.Equal(t, "released", ver.GateState)
	assert.Equal(t, "eval", ver.GatedBy)
	assert.Equal(t, "verified score 82", ver.GateNote)
	require.NotNil(t, ver.GatedAt)
}

func TestReleaseVersionDoesNotRegressPointer(t *testing.T) {
	_, store, ctx := gateFixture(t)
	sk := newGateSkill(t, store, ctx, "no-regress")
	held := newGateVersion(t, store, ctx, sk.ID, "c1", gate.NeedsReview)
	newer := newGateVersion(t, store, ctx, sk.ID, "c2", gate.Allow)

	require.NoError(t, store.ReleaseVersion(ctx, store.Pool(), "no-regress", held.Version, "admin", "approved late"))

	got, err := store.GetByName(ctx, "no-regress")
	require.NoError(t, err)
	assert.Equal(t, newer.Version, got.LatestVersion,
		"releasing an older held version must not pull every client back to it")
}

func TestRejectVersionIsTerminal(t *testing.T) {
	_, store, ctx := gateFixture(t)
	sk := newGateSkill(t, store, ctx, "reject-me")
	v := newGateVersion(t, store, ctx, sk.ID, "c1", gate.NeedsReview)

	require.NoError(t, store.RejectVersion(ctx, "reject-me", v.Version, "admin@example.com", "obfuscated payload, no thanks"))

	ver, err := store.GetVersion(ctx, "reject-me", v.Version)
	require.NoError(t, err)
	assert.Equal(t, "rejected", ver.GateState)

	got, err := store.GetByName(ctx, "reject-me")
	require.NoError(t, err)
	assert.Equal(t, 0, got.LatestVersion, "a rejected version never becomes latest")

	err = store.ReleaseVersion(ctx, store.Pool(), "reject-me", v.Version, "admin", "changed my mind")
	require.Error(t, err, "a rejection is terminal; releasing one must fail loudly rather than silently succeeding")
}

func TestReleaseVersionOnAlreadyReleasedIsANoOpNotAnError(t *testing.T) {
	// Ingestion may re-decide a version that a human already approved.
	// That race must not 500 the worker's report.
	_, store, ctx := gateFixture(t)
	sk := newGateSkill(t, store, ctx, "already-out")
	v := newGateVersion(t, store, ctx, sk.ID, "c1", gate.Allow)
	assert.NoError(t, store.ReleaseVersion(ctx, store.Pool(), "already-out", v.Version, "eval", "score 90"))
}

func TestHeldVersionReturnsOnlyVersionsAwaitingReview(t *testing.T) {
	_, store, ctx := gateFixture(t)
	sk := newGateSkill(t, store, ctx, "held-lookup")
	held := newGateVersion(t, store, ctx, sk.ID, "c1", gate.NeedsReview)
	out := newGateVersion(t, store, ctx, sk.ID, "c2", gate.Allow)

	got, err := store.HeldVersion(ctx, "held-lookup", held.Version)
	require.NoError(t, err)
	assert.Equal(t, "needs_review", got.GateState)

	_, err = store.HeldVersion(ctx, "held-lookup", out.Version)
	require.Error(t, err, "a released version is not awaiting review")

	_, err = store.HeldVersion(ctx, "held-lookup", 99)
	require.Error(t, err, "a version that does not exist is not awaiting review")
}

// TestHeldVersionIsInvisibleToEveryLatestResolvingPath is the invariant of the
// whole phase. Each of these paths resolves "the current version of this
// skill"; a held version must appear in none of them.
func TestHeldVersionIsInvisibleToEveryLatestResolvingPath(t *testing.T) {
	pool, store, ctx := gateFixture(t)
	sk := newGateSkill(t, store, ctx, "invisible")
	_ = newGateVersion(t, store, ctx, sk.ID, "c1", gate.NeedsReview)

	t.Run("list", func(t *testing.T) {
		skills, _, err := store.List(ctx, skill.ListOptions{Limit: 100})
		require.NoError(t, err)
		seen := false
		for _, s := range skills {
			if s.Name == "invisible" {
				seen = true
				assert.Equal(t, 0, s.LatestVersion, "listed with no servable version")
			}
		}
		require.True(t, seen, "the skill itself is still listed; only the held version is withheld")
	})

	t.Run("search", func(t *testing.T) {
		results, err := store.Search(ctx, "invisible", 100)
		require.NoError(t, err)
		for _, s := range results {
			if s.Name == "invisible" {
				assert.Equal(t, 0, s.LatestVersion, "search must not advertise a held version")
			}
		}
	})

	t.Run("sync manifest", func(t *testing.T) {
		m, err := sync.NewStore(pool).GetManifest(ctx)
		require.NoError(t, err)
		for _, e := range m {
			assert.NotEqual(t, "invisible", e.Name, "a held version must never reach a sync manifest")
		}
	})
}
