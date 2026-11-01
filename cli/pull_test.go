package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func packSkill(t *testing.T, body string) []byte {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644))
	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archive
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

type stubSet struct {
	resolve json.RawMessage
	bundle  []byte
}

// serveSet stubs the two routes and points the CLI at them, with HOME empty as
// on a clean machine.
func serveSet(t *testing.T, s stubSet) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sets/team/resolve":
			assert.Equal(t, "true", r.URL.Query().Get("include_excluded"))
			_, _ = w.Write(s.resolve)
		case "/api/sets/team/bundle":
			assert.Equal(t, "res-1", r.URL.Query().Get("resolved"))
			_, _ = w.Write(s.bundle)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SKAEL_URL", srv.URL)
	t.Setenv("SKAEL_KEY", "k")
}

// oneSkillSet builds a resolve body and bundle for skill "alpha" at version 2.
// The unpacked tree in the bundle deliberately differs from the archive.
func oneSkillSet(t *testing.T, archive []byte, resolvedSHA string) stubSet {
	t.Helper()
	sum := sha256.Sum256(archive)
	bundleSHA := hex.EncodeToString(sum[:])
	if resolvedSHA == "" {
		resolvedSHA = bundleSHA
	}
	resolve, err := json.Marshal(map[string]any{
		"id": "res-1", "set": "team", "complete": true,
		"entries": []map[string]any{{"name": "alpha", "version": 2, "sha256": resolvedSHA}},
	})
	require.NoError(t, err)
	manifest, err := json.Marshal(map[string]any{
		"set": "team", "resolution_id": "res-1", "complete": true,
		"skills": []map[string]any{{"name": "alpha", "version": 2, "sha256": resolvedSHA, "archive": ".agents/skills/.archives/alpha@2.tar.gz"}},
	})
	require.NoError(t, err)
	return stubSet{resolve: resolve, bundle: tarGz(t, map[string][]byte{
		".agents/skills/manifest.json":             manifest,
		".agents/skills/.archives/alpha@2.tar.gz":  archive,
		".agents/skills/alpha/SKILL.md":            []byte("unverified tree"),
		".agents/skills/alpha/planted-by-tree.txt": []byte("unverified tree"),
	})}
}

func pullInto(t *testing.T, dir string) (int, string) {
	t.Helper()
	return runCLI(t, "pull", "--set", "team", "--dir", dir, "--no-hooks")
}

func TestPullWritesFromTheVerifiedArchive(t *testing.T) {
	serveSet(t, oneSkillSet(t, packSkill(t, "from the archive\n"), ""))
	dir := t.TempDir()

	code, _ := pullInto(t, dir)
	require.Equal(t, 0, code)

	root := filepath.Join(dir, ".agents", "skills")
	got, err := os.ReadFile(filepath.Join(root, "alpha", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "from the archive\n", string(got))
	assert.NoFileExists(t, filepath.Join(root, "alpha", "planted-by-tree.txt"))
	assert.FileExists(t, filepath.Join(root, "manifest.json"))
	assert.NoDirExists(t, filepath.Join(os.Getenv("HOME"), ".skael"))
}

func TestPullDigestMismatchExits11AndWritesNothing(t *testing.T) {
	serveSet(t, oneSkillSet(t, packSkill(t, "tampered\n"), fmt.Sprintf("%064d", 0)))
	dir := t.TempDir()

	code, _ := pullInto(t, dir)
	assert.Equal(t, 11, code)
	assert.NoDirExists(t, filepath.Join(dir, ".agents"))
}

func TestPullMissingArchiveExits11(t *testing.T) {
	s := oneSkillSet(t, packSkill(t, "x\n"), "")
	s.bundle = tarGz(t, map[string][]byte{".agents/skills/alpha/SKILL.md": []byte("unverified tree")})
	serveSet(t, s)
	dir := t.TempDir()

	code, _ := pullInto(t, dir)
	assert.Equal(t, 11, code)
	assert.NoDirExists(t, filepath.Join(dir, ".agents"))
}

func TestPullIncompleteSetExits10AndWritesNothing(t *testing.T) {
	resolve, err := json.Marshal(map[string]any{
		"id": "res-1", "set": "team", "complete": false, "entries": []any{},
		"excluded": []map[string]any{{"name": "alpha", "requested": "3", "reason": "held"}},
	})
	require.NoError(t, err)
	serveSet(t, stubSet{resolve: resolve})
	dir := t.TempDir()

	code, stdout := runCLI(t, "pull", "--set", "team", "--dir", dir, "--no-hooks", "--json")
	assert.Equal(t, 10, code)
	assert.Contains(t, stdout, `"held"`)
	assert.NoDirExists(t, filepath.Join(dir, ".agents"))
}

// A pull must not delete `skael init`'s skill, and must drop what it no longer
// names, which is why it reads the previous manifest.
func TestPullKeepsForeignEntriesAndDropsStaleSkills(t *testing.T) {
	serveSet(t, oneSkillSet(t, packSkill(t, "new\n"), ""))
	dir := t.TempDir()
	root := filepath.Join(dir, ".agents", "skills")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skael"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skael", "SKILL.md"), []byte("kept\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "stale"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"skills":[{"name":"stale"}]}`), 0o644))

	code, _ := pullInto(t, dir)
	require.Equal(t, 0, code)

	got, err := os.ReadFile(filepath.Join(root, "skael", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "kept\n", string(got))
	assert.NoDirExists(t, filepath.Join(root, "stale"))
	leftovers, err := filepath.Glob(filepath.Join(dir, ".agents", ".skills-staging-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}
