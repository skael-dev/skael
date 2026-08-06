package skill_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

// scanResultWith builds a stored scan_result carrying one finding of the given
// class and severity — the shape the publish route persists.
func scanResultWith(t *testing.T, class scan.Class, severity string) json.RawMessage {
	t.Helper()
	rep := scan.Report{Findings: []scan.Finding{{
		Rule:     "curl-pipe",
		Severity: severity,
		File:     "SKILL.md",
		Line:     4,
		Message:  "piping a download into a shell",
		Class:    string(class),
	}}}
	b, err := json.Marshal(rep)
	require.NoError(t, err)
	return b
}

func TestReconsiderReleasesOnAClearingScore(t *testing.T) {
	_, store, ctx := gateFixture(t)
	releaser := skill.NewReleaser(store)

	sk := newGateSkill(t, store, ctx, "will-clear")
	v, err := store.CreateVersion(ctx, sk.ID, "p", "c", "", "d", "c",
		json.RawMessage(`{}`), nil, scanResultWith(t, gate.ClassExecution, "high"), "t",
		gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{{Rule: "curl-pipe", Class: "execution"}}})
	require.NoError(t, err)

	rec := skill.QualityEvidence{Verified: true, PanelComplete: true, Headline: 82}
	d, released, err := releaser.Reconsider(ctx, store.Pool(), "will-clear", v.Version, rec, 60)
	require.NoError(t, err)
	assert.True(t, released)
	assert.Equal(t, gate.Allow, d.Outcome)

	got, err := store.GetByName(ctx, "will-clear")
	require.NoError(t, err)
	assert.Equal(t, v.Version, got.LatestVersion)

	ver, err := store.GetVersion(ctx, "will-clear", v.Version)
	require.NoError(t, err)
	assert.Equal(t, "released", ver.GateState)
	assert.Equal(t, "system:eval", ver.GatedBy)
}

func TestReconsiderHoldsOnEachFailingCondition(t *testing.T) {
	_, store, ctx := gateFixture(t)
	releaser := skill.NewReleaser(store)

	cases := map[string]skill.QualityEvidence{
		"below floor":       {Verified: true, PanelComplete: true, Headline: 40},
		"unverified":        {Verified: false, PanelComplete: true, Headline: 95},
		"incomplete panel":  {Verified: true, PanelComplete: false, Headline: 95},
		"forbid violations": {Verified: true, PanelComplete: true, Headline: 95, CriticalForbidViolations: 1},
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			skName := "held-" + strings.ReplaceAll(name, " ", "-")
			sk := newGateSkill(t, store, ctx, skName)
			v, err := store.CreateVersion(ctx, sk.ID, "p"+skName, "c"+skName, "", "d", "c",
				json.RawMessage(`{}`), nil, scanResultWith(t, gate.ClassExecution, "high"), "t",
				gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{}})
			require.NoError(t, err)

			d, released, err := releaser.Reconsider(ctx, store.Pool(), skName, v.Version, rec, 60)
			require.NoError(t, err)
			assert.False(t, released, "%s must not release the version", name)
			assert.Equal(t, gate.NeedsReview, d.Outcome, "%s must stay held", name)

			got, err := store.GetByName(ctx, skName)
			require.NoError(t, err)
			assert.Equal(t, 0, got.LatestVersion)
		})
	}
}

func TestReconsiderNeverReleasesABlockedVersion(t *testing.T) {
	// A version whose stored scan carries a secret-class finding cannot exist
	// in needs_review — Block refuses the publish outright. But Reconsider
	// must not be the one place that forgets, because it is the only path
	// that acts on a stored scan report rather than a fresh one.
	_, store, ctx := gateFixture(t)
	releaser := skill.NewReleaser(store)

	sk := newGateSkill(t, store, ctx, "blocked-somehow")
	v, err := store.CreateVersion(ctx, sk.ID, "p", "c", "", "d", "c",
		json.RawMessage(`{}`), nil, scanResultWith(t, gate.ClassSecret, "critical"), "t",
		gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{}})
	require.NoError(t, err)

	d, released, err := releaser.Reconsider(ctx, store.Pool(), "blocked-somehow", v.Version, skill.QualityEvidence{
		Verified: true, PanelComplete: true, Headline: 100,
	}, 0)
	require.NoError(t, err)
	assert.False(t, released)
	assert.Equal(t, gate.Block, d.Outcome)
}

func TestReconsiderIgnoresAReleasedVersion(t *testing.T) {
	// Most evals run on versions that published cleanly. Reconsider must be
	// a cheap no-op for them, not an error and not a redundant write.
	_, store, ctx := gateFixture(t)
	releaser := skill.NewReleaser(store)

	sk := newGateSkill(t, store, ctx, "already-fine")
	v, err := store.CreateVersion(ctx, sk.ID, "p", "c", "", "d", "c",
		json.RawMessage(`{}`), nil, json.RawMessage(`{"status":"clean"}`), "t",
		gate.Decision{Outcome: gate.Allow, Reasons: []gate.Reason{}})
	require.NoError(t, err)

	before, err := store.GetVersion(ctx, "already-fine", v.Version)
	require.NoError(t, err)

	_, released, err := releaser.Reconsider(ctx, store.Pool(), "already-fine", v.Version, skill.QualityEvidence{
		Verified: true, PanelComplete: true, Headline: 90,
	}, 0)
	require.NoError(t, err)
	assert.False(t, released, "an already-released version is not released again")

	after, err := store.GetVersion(ctx, "already-fine", v.Version)
	require.NoError(t, err)
	assert.Equal(t, before.GatedBy, after.GatedBy, "no redundant write")
	assert.Equal(t, before.GateNote, after.GateNote, "no redundant write")
}

func TestReconsiderRejectsAnUnknownVersion(t *testing.T) {
	_, store, ctx := gateFixture(t)
	releaser := skill.NewReleaser(store)
	newGateSkill(t, store, ctx, "no-versions")

	_, released, err := releaser.Reconsider(ctx, store.Pool(), "no-versions", 7, skill.QualityEvidence{
		Verified: true, PanelComplete: true, Headline: 90,
	}, 0)
	require.Error(t, err)
	assert.False(t, released)
}

// TestStoredScanKeepsTheFindingClass is the guard on the whole release path.
// Reconsider re-decides from the scan report stored at publish time. If Class
// did not survive that round trip, gate.Decide would see an unrecognised class
// on every stored finding, fail closed to Block, and no held version could
// ever be released by an evaluation. This publishes a real held version
// through the real route and reads the stored scan back.
func TestStoredScanKeepsTheFindingClass(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	var caller *auth.User
	handler, store := setupTestAPIAsUserWithStore(t, &caller)
	caller = &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}

	createSkill(t, handler, "class-round-trip", "class-round-trip")
	rr := postArchive(t, handler, "/api/skills/class-round-trip/versions",
		appealableBundle(t, "class-round-trip"))
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	ctx := t.Context()
	ver, err := store.GetVersion(ctx, "class-round-trip", 1)
	require.NoError(t, err)
	require.NotNil(t, ver)
	require.Equal(t, "needs_review", ver.GateState)

	var rep scan.Report
	require.NoError(t, json.Unmarshal(ver.ScanResult, &rep))
	require.NotEmpty(t, rep.Findings)

	classes := map[string]bool{}
	for _, f := range rep.Findings {
		assert.NotEmpty(t, f.Class,
			"a stored finding with no class fails closed to Block and makes the hold permanent: %+v", f)
		classes[f.Class] = true
	}
	assert.True(t, classes[string(scan.ClassExecution)],
		"the stored scan must still carry the execution class that made this version appealable; got %v", classes)

	// And the decision re-run from the stored scan must actually clear.
	releaser := skill.NewReleaser(store)
	d, released, err := releaser.Reconsider(ctx, store.Pool(), "class-round-trip", 1,
		skill.QualityEvidence{Verified: true, PanelComplete: true, Headline: 90}, 60)
	require.NoError(t, err)
	assert.Equal(t, gate.Allow, d.Outcome)
	assert.True(t, released, "a verified score must clear a version held on a stored appealable finding")
}
