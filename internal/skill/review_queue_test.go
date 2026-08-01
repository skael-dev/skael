package skill_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// setupReviewQueueTestAPI is setupTestAPI plus the cross-skill review queue
// route, which is registered separately from skill.RegisterRoutes because it
// is not scoped to a single skill.
func setupReviewQueueTestAPI(t *testing.T) (http.Handler, *skill.Store) {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{})
	skill.RegisterReviewQueueRoutes(api, store)

	return r, store
}

func TestListHeldVersions_OnlyNeedsReview(t *testing.T) {
	_, store, ctx := gateFixture(t)

	held := newGateSkill(t, store, ctx, "held-one")
	newGateVersion(t, store, ctx, held.ID, "c1", gate.NeedsReview)

	released := newGateSkill(t, store, ctx, "released-one")
	newGateVersion(t, store, ctx, released.ID, "c2", gate.Allow)

	rejected := newGateSkill(t, store, ctx, "rejected-one")
	rv := newGateVersion(t, store, ctx, rejected.ID, "c3", gate.NeedsReview)
	require.NoError(t, store.RejectVersion(ctx, "rejected-one", rv.Version, "admin@example.com", "no thanks"))

	got, err := store.ListHeldVersions(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "held-one", got[0].SkillName)
}

// TestListHeldVersions_CarriesTheGateDecision pins the review screen's
// contract: Reasons[].Clears tells the reader which resolution paths are
// open for each finding, and must survive the round trip intact.
func TestListHeldVersions_CarriesTheGateDecision(t *testing.T) {
	_, store, ctx := gateFixture(t)

	sk := newGateSkill(t, store, ctx, "held-two")
	v, err := store.CreateVersion(ctx, sk.ID, "p/c1", "c1", "", "d", "c",
		json.RawMessage(`{}`), nil, json.RawMessage(`{"findings":[{"rule":"DATA_EXFILTRATION"}]}`), "t",
		gate.Decision{
			Outcome: gate.NeedsReview,
			Reasons: []gate.Reason{
				{Rule: "DATA_EXFILTRATION", Clears: "a verified evaluation or an admin approval"},
			},
		})
	require.NoError(t, err)
	require.Equal(t, "needs_review", v.GateState)

	got, err := store.ListHeldVersions(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)

	var decision struct {
		Reasons []struct {
			Rule   string `json:"rule"`
			Clears string `json:"clears"`
		} `json:"reasons"`
	}
	require.NoError(t, json.Unmarshal(got[0].GateDecision, &decision))
	require.Len(t, decision.Reasons, 1)
	assert.Equal(t, "a verified evaluation or an admin approval", decision.Reasons[0].Clears)

	// The scan findings that caused the hold ride along too.
	assert.Contains(t, string(got[0].ScanResult), "DATA_EXFILTRATION")
}

func TestReviewQueueRoute_ReturnsHeldVersions(t *testing.T) {
	handler, store := setupReviewQueueTestAPI(t)
	ctx := t.Context()

	sk, err := store.Create(ctx, "held-three", "held-three", "a held fixture", "content", json.RawMessage(`{}`))
	require.NoError(t, err)
	_, err = store.CreateVersion(ctx, sk.ID, "p/c1", "c1", "", "d", "c",
		json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t",
		gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{}})
	require.NoError(t, err)

	rr := doJSON(t, handler, http.MethodGet, "/api/review/queue", nil, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var body struct {
		Held []struct {
			SkillName string `json:"skill_name"`
			Version   int    `json:"version"`
		} `json:"held"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, 1, body.Total)
	require.Len(t, body.Held, 1)
	assert.Equal(t, "held-three", body.Held[0].SkillName)
}

// TestListHeldVersions_NullDecisionAndScanResultAreRejectedBySchema proves,
// rather than asserts in prose, that ListHeldVersions's nil-tolerant scan of
// gate_decision/scan_result is defensive against a state the schema itself
// forbids: both columns are `JSONB NOT NULL DEFAULT '{}'`
// (internal/platform/migrate/001_initial.sql, 013_publish_gate.sql), so an
// INSERT carrying a literal SQL NULL in either column is rejected by
// Postgres before ListHeldVersions ever sees the row. This is checked
// directly, bypassing CreateVersion, because CreateVersion would never
// produce this row shape either.
func TestListHeldVersions_NullDecisionAndScanResultAreRejectedBySchema(t *testing.T) {
	pool, store, ctx := gateFixture(t)
	sk := newGateSkill(t, store, ctx, "null-columns")

	const insertNullDecision = `
		INSERT INTO skill_versions
			(skill_id, version, archive_path, checksum, gate_state, gate_decision, scan_result)
		VALUES ($1, 1, 'p/null', 'null-c', 'needs_review', NULL, '{}')`
	_, err := pool.Exec(ctx, insertNullDecision, sk.ID)
	require.Error(t, err, "gate_decision is NOT NULL; a literal NULL insert must fail at the DB")
	assert.Contains(t, err.Error(), "null value in column")

	const insertNullScanResult = `
		INSERT INTO skill_versions
			(skill_id, version, archive_path, checksum, gate_state, gate_decision, scan_result)
		VALUES ($1, 1, 'p/null', 'null-c', 'needs_review', '{}', NULL)`
	_, err = pool.Exec(ctx, insertNullScanResult, sk.ID)
	require.Error(t, err, "scan_result is NOT NULL; a literal NULL insert must fail at the DB")
	assert.Contains(t, err.Error(), "null value in column")

	// Since no such row can exist, ListHeldVersions has nothing to choke on;
	// confirm the listing still runs clean over whatever legitimately-shaped
	// held rows do exist (none, here).
	got, err := store.ListHeldVersions(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestReviewQueueRoute_EmptyIsEmptyArray pins the []HeldVersion{} choice: a
// nil slice marshals to `null`, and the sidebar count and empty state both
// read this response's held field with .length.
func TestReviewQueueRoute_EmptyIsEmptyArray(t *testing.T) {
	handler, _ := setupReviewQueueTestAPI(t)

	rr := doJSON(t, handler, http.MethodGet, "/api/review/queue", nil, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"held":[]`)
}
