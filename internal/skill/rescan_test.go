package skill_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// A version released before a rule existed is withdrawn by the rescan that
// finds it: it is never served again, and the pointer falls back to the
// released version below it.
func TestRescanWithdrawsAReleasedVersionAndOnlyAnOwnerMayRescan(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)
	seedCanonicalTestUsers(t, pool)
	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	var caller *auth.User
	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if caller != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), caller))
			}
			next.ServeHTTP(w, req)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	owners := &namespaceOwner{ownerEmail: "admin@example.com"}
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{Ownership: owners})

	admin := &auth.User{ID: "00000000-0000-0000-0000-000000000002", Email: "admin@example.com", Role: auth.RoleAdmin}
	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}

	caller = admin
	createSkill(t, r, "deploy-helper", "deploys")
	publishVersion(t, r, "deploy-helper", buildTestArchive(t, "deploy-helper", "deploys"))

	// v2 carries a live credential but was stored with a clean report, as a
	// release made before the rule that catches it.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(
		"---\nname: deploy-helper\ndescription: deploys\n---\n# deploy\n\n"+
			"client = Anthropic(api_key=\"sk-ant-api03-Ab3xK9pQ2rTvWyZ1mN4jH7gF5dS8aE6cB0\")\n"), 0o644))
	archive, checksum, _, err := skill.Pack(dir)
	require.NoError(t, err)
	_, err = storage.Write(ctx, "deploy-helper/v2.tar.gz", bytes.NewReader(archive))
	require.NoError(t, err)
	sk, err := store.GetByName(ctx, "deploy-helper")
	require.NoError(t, err)
	_, err = store.CreateVersion(ctx, sk.ID, "deploy-helper/v2.tar.gz", checksum, "", "deploys", "# deploy",
		json.RawMessage(`{}`), nil, json.RawMessage(`{"status":"clean","findings":[],"summary":{}}`),
		"admin@example.com", gate.Decision{Outcome: gate.Allow})
	require.NoError(t, err)

	caller = member
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/skills/deploy-helper/versions/2/scan", nil))
	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())

	caller = admin
	var res skill.SkillScan
	rr = doJSON(t, r, http.MethodPost, "/api/skills/deploy-helper/versions/2/scan", nil, &res)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, skill.RescanWithdrawn, res.Outcome)
	require.Equal(t, "withdrawn", res.GateState)

	sk, err = store.GetByName(ctx, "deploy-helper")
	require.NoError(t, err)
	require.Equal(t, 1, sk.LatestVersion)

	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/skills/deploy-helper/versions/2/download", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)

	var view skill.SkillScan
	rr = doJSON(t, r, http.MethodGet, "/api/skills/deploy-helper/scan", nil, &view)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, 2, view.Version, "the tab shows the newest version, withdrawn or not")
	require.NotEmpty(t, view.NewFindings, "the credential is new since release")
}
