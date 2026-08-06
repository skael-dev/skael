package skillimport

import (
	"context"
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

// fakeOwners is a minimal skill.OwnerResolver fake, mirroring the one in
// internal/skill/publish_ownership_test.go — it lives here too because that
// one is unexported in package skill_test and unreachable from skillimport.
type fakeOwners struct {
	state   gate.OwnerState
	claimed []string
}

func (f *fakeOwners) ResolveForPublish(_ context.Context, _ string, _ *auth.User) (gate.OwnerState, error) {
	return f.state, nil
}

func (f *fakeOwners) ClaimOnFirstPublish(_ context.Context, name string, _ *auth.User) error {
	f.claimed = append(f.claimed, name)
	return nil
}

// setupImportTestAPIWithOwnership mirrors setupImportTestAPI (publish_gate_test.go)
// but wires the given ownership resolver into RegisterRoutes.
func setupImportTestAPIWithOwnership(t *testing.T, tarball []byte, owners skill.OwnerResolver) http.Handler {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	skillStore := skill.NewStore(pool)
	importStore := NewStore(pool)

	storageDir := t.TempDir()
	storage, err := platform.NewLocalStorage(storageDir)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(tarball)
	}))
	t.Cleanup(srv.Close)

	fetcher := NewFetcher(srv.URL, "")

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, skillStore, storage, skill.RouteOptions{})
	RegisterRoutes(api, r, importStore, skillStore, storage, fetcher, RouteOptions{Ownership: owners})

	return r
}

// O6 applies to import too: a newly imported skill claims its importer,
// unless a rule already covers the name.
func TestImportClaimsOwnershipOfNewSkills(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	owners := &fakeOwners{state: gate.OwnerState{Evaluated: true, Unowned: true}}

	tarball := makeRepoTarball(t, "acme-skills-abc1234", map[string]string{
		"claim-me-a": skillMD("claim-me-a", "a clean skill", "Nothing unusual here."),
		"claim-me-b": skillMD("claim-me-b", "another clean skill", "Nothing unusual here either."),
	})

	handler := setupImportTestAPIWithOwnership(t, tarball, owners)

	rr := doImportPost(t, handler, []string{"claim-me-a", "claim-me-b"})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	assert.ElementsMatch(t, []string{"claim-me-a", "claim-me-b"}, owners.claimed,
		"a brand-new skill must claim its importer as owner, mirroring publish's O6 behaviour")
}

func TestImportUnderAnExistingRuleDoesNotClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	owners := &fakeOwners{state: gate.OwnerState{
		Evaluated: true, IsOwner: false, RulePattern: "payments:*",
		Owners: []gate.OwnerRef{{ID: "u1", Email: "alice@acme.com"}},
	}}

	tarball := makeRepoTarball(t, "acme-skills-abc1234", map[string]string{
		"brandnew": skillMD("brandnew", "a clean skill", "Nothing unusual here."),
	})

	handler := setupImportTestAPIWithOwnership(t, tarball, owners)

	rr := doImportPost(t, handler, []string{"brandnew"})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	assert.Empty(t, owners.claimed, "a covering rule must win over first-import claim")

	var sk skill.Skill
	getSkillJSON(t, handler, "brandnew", &sk)
	assert.Equal(t, 0, sk.LatestVersion, "held for review: not owned, no rule-clearing evidence yet")
}
