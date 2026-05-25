package skillimport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		tw.WriteHeader(hdr)
		tw.Write([]byte(content))
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestFetch_ExtractsToTempDir(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"anthropics-skills-abc1234/skills/my-skill/SKILL.md":      "---\nname: my-skill\n---\nHello",
		"anthropics-skills-abc1234/skills/my-skill/refs/guide.md": "# Guide",
		"anthropics-skills-abc1234/README.md":                     "# Repo readme",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		w.Write(tarball)
	}))
	defer srv.Close()

	f := NewFetcher(srv.URL, "")
	result, err := f.Fetch(Source{Type: "github", Owner: "anthropics", Repo: "skills", Ref: "main"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	if result.CommitSHA != "abc1234" {
		t.Errorf("CommitSHA = %q, want %q", result.CommitSHA, "abc1234")
	}

	skillMD := filepath.Join(result.Dir, "skills", "my-skill", "SKILL.md")
	if _, err := os.Stat(skillMD); err != nil {
		t.Errorf("expected %s to exist: %v", skillMD, err)
	}
}

func TestFetch_UsesToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		tarball := makeTarball(t, map[string]string{
			"o-r-abc1234/SKILL.md": "hi",
		})
		w.Write(tarball)
	}))
	defer srv.Close()

	f := NewFetcher(srv.URL, "ghp_testtoken123")
	result, err := f.Fetch(Source{Type: "github", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	if gotAuth != "Bearer ghp_testtoken123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer ghp_testtoken123")
	}
}
