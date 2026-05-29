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

func TestFetch_SkipsSymlinks(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Root directory
	tw.WriteHeader(&tar.Header{Name: "obra-superpowers-abc1234/", Typeflag: tar.TypeDir, Mode: 0755})
	// Symlink entry (like AGENTS.md -> CLAUDE.md in obra/superpowers)
	tw.WriteHeader(&tar.Header{Name: "obra-superpowers-abc1234/AGENTS.md", Typeflag: tar.TypeSymlink, Linkname: "CLAUDE.md"})
	// Regular files
	skillContent := []byte("---\nname: systematic-debugging\n---\nDebug skill")
	tw.WriteHeader(&tar.Header{Name: "obra-superpowers-abc1234/skills/systematic-debugging/SKILL.md", Mode: 0644, Size: int64(len(skillContent)), Typeflag: tar.TypeReg})
	tw.Write(skillContent)
	refContent := []byte("# Reference")
	tw.WriteHeader(&tar.Header{Name: "obra-superpowers-abc1234/skills/systematic-debugging/reference.md", Mode: 0644, Size: int64(len(refContent)), Typeflag: tar.TypeReg})
	tw.Write(refContent)
	tw.Close()
	gw.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	f := NewFetcher(srv.URL, "")
	result, err := f.Fetch(Source{Type: "github", Owner: "obra", Repo: "superpowers", Ref: "main"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	// Symlink should be skipped, not extracted
	if _, err := os.Lstat(filepath.Join(result.Dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md symlink should not exist, got err: %v", err)
	}

	// Regular skill files should be extracted
	skillMD := filepath.Join(result.Dir, "skills", "systematic-debugging", "SKILL.md")
	if _, err := os.Stat(skillMD); err != nil {
		t.Errorf("SKILL.md should exist: %v", err)
	}
	refMD := filepath.Join(result.Dir, "skills", "systematic-debugging", "reference.md")
	if _, err := os.Stat(refMD); err != nil {
		t.Errorf("reference.md should exist: %v", err)
	}

	// Discover should find the skill
	skills, err := Discover(result.Dir, "skills/systematic-debugging")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	if skills[0].Name != "systematic-debugging" {
		t.Errorf("name = %q, want %q", skills[0].Name, "systematic-debugging")
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
