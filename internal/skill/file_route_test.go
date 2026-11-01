package skill_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/skill"
)

func TestFileRoute(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	caller := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}
	handler := setupTestAPIAsUser(t, &caller)

	get := func(key *auth.KeyInfo, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(auth.ContextWithKey(req.Context(), key))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}
	scoped := &auth.KeyInfo{ID: "key", Scopes: []string{auth.ScopeSkillsRead}}

	// A released version with a nested file.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: files\ndescription: d\n---\n# files\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "run.html"), []byte("<script>1</script>"), 0o644))
	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	createSkill(t, handler, "files", "d")
	require.Equal(t, http.StatusCreated, postArchive(t, handler, "/api/skills/files/versions", archive).Code)

	// A second version held by the gate.
	held := postArchive(t, handler, "/api/skills/files/versions", appealableBundle(t, "files"))
	require.Equal(t, http.StatusCreated, held.Code, held.Body.String())
	require.Contains(t, held.Body.String(), `"gate_state":"needs_review"`)

	t.Run("serves untrusted bytes as an opaque attachment", func(t *testing.T) {
		rr := get(scoped, "/api/skills/files/versions/1/files/scripts/run.html")
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		assert.Equal(t, "<script>1</script>", rr.Body.String())
		assert.Equal(t, "application/octet-stream", rr.Header().Get("Content-Type"))
		assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "18", rr.Header().Get("X-Skael-File-Size"))
		assert.Contains(t, rr.Header().Get("Content-Disposition"), "attachment")
	})

	t.Run("a held version reaches a member and not a skills:read key", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, get(nil, "/api/skills/files/versions/2/files/SKILL.md").Code)
		assert.Equal(t, http.StatusNotFound, get(scoped, "/api/skills/files/versions/2/files/SKILL.md").Code)
	})

	t.Run("rejects traversal and reports a missing file", func(t *testing.T) {
		for _, p := range []string{"../../etc/passwd", "scripts/../../SKILL.md", "%2e%2e/%2e%2e/etc/passwd", "..%2f..%2fetc%2fpasswd", "/etc/passwd"} {
			assert.Equal(t, http.StatusBadRequest, get(nil, "/api/skills/files/versions/1/files/"+p).Code, p)
		}
		assert.Equal(t, http.StatusNotFound, get(nil, "/api/skills/files/versions/1/files/missing.md").Code)
		assert.Equal(t, http.StatusNotFound, get(nil, "/api/skills/files/versions/1/files/scripts").Code)
	})
}
