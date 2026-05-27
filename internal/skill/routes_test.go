package skill_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// setupTestAPI creates a real Chi router + Huma API backed by a real ephemeral
// Postgres database. Returns the http.Handler, the Store (for direct DB checks),
// and the Storage (for archive access).
func setupTestAPI(t *testing.T) (http.Handler, *skill.Store, *platform.Storage) {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)

	storageDir := t.TempDir()
	storage, err := platform.NewStorage(storageDir)
	require.NoError(t, err)

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage)

	return r, store, storage
}

// doJSON sends a JSON-body request and returns the recorder. It decodes the
// response body into out only when out is non-nil.
func doJSON(t *testing.T, handler http.Handler, method, path string, body interface{}, out interface{}) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if out != nil {
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), out))
	}
	return rr
}

// createSkill is a convenience that creates a skill and asserts 201.
func createSkill(t *testing.T, handler http.Handler, name, description string) skill.Skill {
	t.Helper()
	var resp skill.Skill
	rr := doJSON(t, handler, http.MethodPost, "/api/skills", map[string]string{
		"name":        name,
		"description": description,
	}, &resp)
	require.Equal(t, http.StatusCreated, rr.Code,
		"create skill %q: %s", name, rr.Body.String())
	return resp
}

// buildTestArchive creates a temp dir with a SKILL.md and returns packed archive bytes.
func buildTestArchive(t *testing.T, skillName, description string) []byte {
	t.Helper()

	dir := t.TempDir()
	skillMD := strings.Join([]string{
		"---",
		"name: " + skillName,
		"description: " + description,
		"---",
		"# " + skillName,
		"This is the skill body.",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0644))

	archiveBytes, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archiveBytes
}

// publishVersion posts a tar.gz archive and asserts 201. Returns the Version.
func publishVersion(t *testing.T, handler http.Handler, skillName string, archiveBytes []byte) skill.Version {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/skills/"+skillName+"/versions",
		bytes.NewReader(archiveBytes))
	req.Header.Set("Content-Type", "application/gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code,
		"publish version for %q: %s", skillName, rr.Body.String())

	var ver skill.Version
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ver))
	return ver
}

// -----------------------------------------------------------------
// POST /api/skills — create
// -----------------------------------------------------------------

func TestCreateSkill_201(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	sk := createSkill(t, handler, "my-skill", "A test skill")
	require.Equal(t, "my-skill", sk.Name)
	require.Equal(t, "A test skill", sk.Description)
	require.NotEmpty(t, sk.ID)
}

func TestCreateSkill_NoDescription_201(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	// description is omitempty so this should succeed.
	var resp skill.Skill
	rr := doJSON(t, handler, http.MethodPost, "/api/skills",
		map[string]string{"name": "minimal-skill"}, &resp)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	require.Equal(t, "minimal-skill", resp.Name)
}

func TestCreateSkill_409_Duplicate(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "dup-skill", "first")

	// Second create with same name → 409.
	rr := doJSON(t, handler, http.MethodPost, "/api/skills",
		map[string]string{"name": "dup-skill", "description": "second"}, nil)
	require.Equal(t, http.StatusConflict, rr.Code)
}

// -----------------------------------------------------------------
// GET /api/skills/{name}
// -----------------------------------------------------------------

func TestGetSkill_200(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "get-skill", "A gettable skill")

	var resp skill.Skill
	rr := doJSON(t, handler, http.MethodGet, "/api/skills/get-skill", nil, &resp)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "get-skill", resp.Name)
}

func TestGetSkill_404(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	rr := doJSON(t, handler, http.MethodGet, "/api/skills/nonexistent", nil, nil)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// -----------------------------------------------------------------
// GET /api/skills — list
// -----------------------------------------------------------------

func TestListSkills_200(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "alpha", "first")
	createSkill(t, handler, "beta", "second")
	createSkill(t, handler, "gamma", "third")

	var resp struct {
		Skills []skill.Skill `json:"skills"`
		Total  int           `json:"total"`
	}
	rr := doJSON(t, handler, http.MethodGet, "/api/skills", nil, &resp)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 3, resp.Total)
	require.Len(t, resp.Skills, 3)
}

func TestListSkills_EmptyDB(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	var resp struct {
		Skills []skill.Skill `json:"skills"`
		Total  int           `json:"total"`
	}
	rr := doJSON(t, handler, http.MethodGet, "/api/skills", nil, &resp)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 0, resp.Total)
	require.NotNil(t, resp.Skills)
}

func TestListSkills_Pagination(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	for _, name := range []string{"skill-1", "skill-2", "skill-3", "skill-4", "skill-5"} {
		createSkill(t, handler, name, "paginated")
	}

	var resp struct {
		Skills []skill.Skill `json:"skills"`
		Total  int           `json:"total"`
	}
	rr := doJSON(t, handler, http.MethodGet, "/api/skills?limit=2&offset=0", nil, &resp)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 5, resp.Total)
	require.Len(t, resp.Skills, 2)
}

// -----------------------------------------------------------------
// DELETE /api/skills/{name}
// -----------------------------------------------------------------

func TestDeleteSkill_204(t *testing.T) {
	handler, store, _ := setupTestAPI(t)

	createSkill(t, handler, "to-delete", "temporary")

	req := httptest.NewRequest(http.MethodDelete, "/api/skills/to-delete", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Confirm it is gone from DB.
	sk, err := store.GetByName(context.Background(), "to-delete")
	require.NoError(t, err)
	require.Nil(t, sk)
}

func TestDeleteSkill_404(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/skills/nobody", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// -----------------------------------------------------------------
// POST /api/skills/{name}/versions — publish
// -----------------------------------------------------------------

func TestPublishVersion_201(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "pub-skill", "A published skill")
	archiveBytes := buildTestArchive(t, "pub-skill", "A published skill")

	ver := publishVersion(t, handler, "pub-skill", archiveBytes)

	require.Equal(t, 1, ver.Version)
	require.NotEmpty(t, ver.Checksum)
	require.NotEmpty(t, ver.ID)
}

func TestPublishVersion_404_NoSkill(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	archiveBytes := buildTestArchive(t, "ghost-skill", "nonexistent")
	req := httptest.NewRequest(http.MethodPost, "/api/skills/ghost-skill/versions",
		bytes.NewReader(archiveBytes))
	req.Header.Set("Content-Type", "application/gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestPublishVersion_400_InvalidArchive(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "bad-archive-skill", "test")

	req := httptest.NewRequest(http.MethodPost, "/api/skills/bad-archive-skill/versions",
		bytes.NewReader([]byte("this is not a valid gzip archive")))
	req.Header.Set("Content-Type", "application/gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestPublishVersion_UpdatesSkillContent(t *testing.T) {
	handler, store, _ := setupTestAPI(t)

	createSkill(t, handler, "content-skill", "old description")
	archiveBytes := buildTestArchive(t, "content-skill", "updated description from frontmatter")
	publishVersion(t, handler, "content-skill", archiveBytes)

	// The skill's description and content should be updated.
	sk, err := store.GetByName(context.Background(), "content-skill")
	require.NoError(t, err)
	require.NotNil(t, sk)
	require.Equal(t, "updated description from frontmatter", sk.Description)
	require.Equal(t, 1, sk.LatestVersion)
}

// -----------------------------------------------------------------
// GET /api/skills/{name}/versions — list versions
// -----------------------------------------------------------------

func TestListVersions_200(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "versioned-skill", "versioning test")

	// Publish two versions with different content so checksums differ.
	for i := 0; i < 2; i++ {
		archiveBytes := buildTestArchive(t, "versioned-skill", fmt.Sprintf("desc v%d", i+1))
		publishVersion(t, handler, "versioned-skill", archiveBytes)
	}

	var resp struct {
		Versions []skill.Version `json:"versions"`
	}
	rr := doJSON(t, handler, http.MethodGet, "/api/skills/versioned-skill/versions", nil, &resp)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, resp.Versions, 2)
	// Versions come back DESC.
	require.Equal(t, 2, resp.Versions[0].Version)
	require.Equal(t, 1, resp.Versions[1].Version)
}

func TestListVersions_404_NoSkill(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	rr := doJSON(t, handler, http.MethodGet, "/api/skills/no-such-skill/versions", nil, nil)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// -----------------------------------------------------------------
// GET /api/skills/{name}/versions/{version}/download
// -----------------------------------------------------------------

func TestDownloadVersion(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "dl-skill", "download test")
	archiveBytes := buildTestArchive(t, "dl-skill", "download test")
	publishVersion(t, handler, "dl-skill", archiveBytes)

	req := httptest.NewRequest(http.MethodGet, "/api/skills/dl-skill/versions/1/download", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, "application/gzip", rr.Header().Get("Content-Type"))
	require.NotEmpty(t, rr.Body.Bytes())
}

// -----------------------------------------------------------------
// GET /api/skills/{name}/scan
// -----------------------------------------------------------------

func TestGetScanResult(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "scan-skill", "scan test")
	archiveBytes := buildTestArchive(t, "scan-skill", "scan test")
	publishVersion(t, handler, "scan-skill", archiveBytes)

	req := httptest.NewRequest(http.MethodGet, "/api/skills/scan-skill/scan", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var report map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&report))
	require.Contains(t, report, "status")
}

func TestGetScanResult_NoVersions(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	createSkill(t, handler, "unscannable", "no versions")

	req := httptest.NewRequest(http.MethodGet, "/api/skills/unscannable/scan", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

// TestDownloadVersion_SkillNotFound verifies that downloading a version for a
// nonexistent skill returns 404.
func TestDownloadVersion_SkillNotFound(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/skills/ghost-skill/versions/1/download", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

// TestGetScanResult_SkillNotFound verifies that requesting scan results for a
// nonexistent skill returns 404.
func TestGetScanResult_SkillNotFound(t *testing.T) {
	handler, _, _ := setupTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/skills/no-such-skill/scan", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

// -----------------------------------------------------------------
// DELETE /api/skills/{name} — archive cleanup
// -----------------------------------------------------------------

// TestDeleteSkill_CleansUpArchive verifies that deleting a skill also removes
// its published archive file from storage.
func TestDeleteSkill_CleansUpArchive(t *testing.T) {
	handler, store, storage := setupTestAPI(t)

	// Create a skill, publish one version so an archive file is written.
	createSkill(t, handler, "cleanup-skill", "will be deleted")
	archiveBytes := buildTestArchive(t, "cleanup-skill", "will be deleted")
	publishVersion(t, handler, "cleanup-skill", archiveBytes)

	// Use the store to retrieve the version (includes ArchivePath which is json:"-").
	versions, err := store.ListVersions(context.Background(), "cleanup-skill")
	require.NoError(t, err)
	require.Len(t, versions, 1)
	archivePath := versions[0].ArchivePath
	require.NotEmpty(t, archivePath, "published version should have an archive path")

	// Confirm the archive file exists in storage before deletion.
	rc, err := storage.Read(archivePath)
	require.NoError(t, err, "archive should exist in storage before delete")
	rc.Close()

	// Delete the skill.
	req := httptest.NewRequest(http.MethodDelete, "/api/skills/cleanup-skill", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Confirm the skill record is gone from DB.
	sk, err := store.GetByName(context.Background(), "cleanup-skill")
	require.NoError(t, err)
	require.Nil(t, sk)

	// Confirm the archive file has been removed from storage.
	_, err = storage.Read(archivePath)
	require.Error(t, err, "archive file should have been deleted from storage")
}

// -----------------------------------------------------------------
// POST /api/skills — skill name validation
// -----------------------------------------------------------------

// TestCreateSkill_NameValidation exercises the name regex: verifies that invalid
// names are rejected with 422 and valid names are accepted with 201.
func TestCreateSkill_NameValidation(t *testing.T) {
	cases := []struct {
		name       string
		wantStatus int
	}{
		// Rejected names
		{"My-Skill", http.StatusUnprocessableEntity},  // uppercase
		{"skill name", http.StatusUnprocessableEntity}, // space
		{"skill-", http.StatusUnprocessableEntity},     // trailing hyphen
		{"-skill", http.StatusUnprocessableEntity},     // leading hyphen
		{"", http.StatusUnprocessableEntity},           // empty
		// Accepted names
		{"valid", http.StatusCreated},
		{"my-skill", http.StatusCreated},
		{"a", http.StatusCreated},
		{"a1b2", http.StatusCreated},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _ := setupTestAPI(t)
			rr := doJSON(t, handler, http.MethodPost, "/api/skills",
				map[string]string{"name": tc.name, "description": "test"}, nil)
			require.Equal(t, tc.wantStatus, rr.Code,
				"name=%q body=%s", tc.name, rr.Body.String())
		})
	}
}
