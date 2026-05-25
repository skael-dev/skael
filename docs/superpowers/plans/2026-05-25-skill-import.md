# Skill Import Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a unified import pipeline that brings skills from GitHub repos, local directories, and skills.sh into the Skael registry via CLI, API, and web UI.

**Architecture:** New `internal/import/` package handles source resolution, GitHub fetching, and skill discovery. Import reuses the existing `skill.Pack`, `skill.Store.CreateVersion`, and `scan.ScanDir` infrastructure — no changes to the publish pipeline. A new `import_sources` table tracks provenance. The CLI adds a `skael import` command with Lipgloss-styled output. The web UI adds an import modal to the skill list page.

**Tech Stack:** Go (net/http for GitHub API, existing pgx/Huma/Chi patterns), React (existing Dialog/Checkbox components, react-query mutations), Lipgloss (CLI styling)

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/platform/migrate/002_import_sources.sql` | Migration for `import_sources` table |
| Create | `internal/import/source.go` | Source struct and GitHub URL resolver |
| Create | `internal/import/source_test.go` | URL parsing tests |
| Create | `internal/import/fetch.go` | GitHub tarball fetcher |
| Create | `internal/import/fetch_test.go` | Fetcher tests (with httptest) |
| Create | `internal/import/discover.go` | Walk tree for SKILL.md directories |
| Create | `internal/import/discover_test.go` | Discovery tests |
| Create | `internal/import/store.go` | Import source CRUD (pgx) |
| Create | `internal/import/store_test.go` | Store tests (testcontainers) |
| Create | `internal/import/routes.go` | Huma route handlers for resolve/import/upload/sources |
| Create | `internal/import/routes_test.go` | HTTP integration tests |
| Modify | `internal/platform/config.go` | Add `GitHubToken` field |
| Modify | `cmd/server/main.go` | Register import routes |
| Create | `cli/import.go` | CLI `skael import` command with Lipgloss UI |
| Create | `cli/client/import.go` | Client methods for import API endpoints |
| Create | `web/src/features/import/import-modal.tsx` | Import modal component |
| Modify | `web/src/features/skills/skill-list.tsx` | Add import button |
| Modify | `web/src/features/skills/skill-detail.tsx` | Show import provenance |

---

### Task 1: Migration — `import_sources` table

**Files:**
- Create: `internal/platform/migrate/002_import_sources.sql`

- [ ] **Step 1: Write the migration file**

```sql
-- +goose Up
CREATE TABLE import_sources (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id     UUID NOT NULL UNIQUE REFERENCES skills(id) ON DELETE CASCADE,
    source_type  TEXT NOT NULL,
    source_url   TEXT NOT NULL DEFAULT '',
    source_path  TEXT NOT NULL DEFAULT '',
    source_ref   TEXT NOT NULL DEFAULT '',
    commit_sha   TEXT NOT NULL DEFAULT '',
    imported_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked TIMESTAMPTZ
);

CREATE INDEX idx_import_sources_skill_id ON import_sources(skill_id);

-- +goose Down
DROP TABLE IF EXISTS import_sources;
```

- [ ] **Step 2: Verify migration applies**

Run: `just migrate`
Expected: Migration 002 applies cleanly. Verify with `just migrate-status`.

- [ ] **Step 3: Verify rollback works**

Run: `just migrate-down`
Expected: Table dropped. Run `just migrate` again to re-apply.

- [ ] **Step 4: Commit**

```bash
git add internal/platform/migrate/002_import_sources.sql
git commit -m "feat(import): add import_sources migration"
```

---

### Task 2: Source resolver — parse GitHub URLs

**Files:**
- Create: `internal/import/source.go`
- Create: `internal/import/source_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/import/source_test.go
package skillimport

import (
	"testing"
)

func TestResolveGitHubURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Source
		wantErr bool
	}{
		{
			name:  "full URL with tree path",
			input: "https://github.com/anthropics/skills/tree/main/skills/skill-creator",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "main",
				Path:  "skills/skill-creator",
			},
		},
		{
			name:  "repo root only",
			input: "https://github.com/anthropics/skills",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "",
				Path:  "",
			},
		},
		{
			name:  "no scheme",
			input: "github.com/anthropics/skills",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "",
				Path:  "",
			},
		},
		{
			name:  "tag ref",
			input: "https://github.com/anthropics/skills/tree/v1.2.0",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "v1.2.0",
				Path:  "",
			},
		},
		{
			name:  "trailing slash stripped",
			input: "https://github.com/anthropics/skills/",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "",
				Path:  "",
			},
		},
		{
			name:    "not github",
			input:   "https://gitlab.com/foo/bar",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Type != tt.want.Type || got.Owner != tt.want.Owner || got.Repo != tt.want.Repo || got.Ref != tt.want.Ref || got.Path != tt.want.Path {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/import/ -run TestResolveGitHubURL -v`
Expected: Compilation error — package and function don't exist yet.

- [ ] **Step 3: Write the implementation**

```go
// internal/import/source.go
package skillimport

import (
	"fmt"
	"net/url"
	"strings"
)

type Source struct {
	Type      string `json:"type"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	CommitSHA string `json:"commit_sha"`
}

func ResolveURL(raw string) (Source, error) {
	if raw == "" {
		return Source{}, fmt.Errorf("empty URL")
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Source{}, fmt.Errorf("parse URL: %w", err)
	}

	if u.Host != "github.com" {
		return Source{}, fmt.Errorf("unsupported host %q (only github.com is supported)", u.Host)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return Source{}, fmt.Errorf("expected github.com/owner/repo, got %q", u.Path)
	}

	s := Source{
		Type:  "github",
		Owner: parts[0],
		Repo:  parts[1],
	}

	// Format: /owner/repo/tree/ref[/path...]
	if len(parts) >= 4 && parts[2] == "tree" {
		s.Ref = parts[3]
		if len(parts) > 4 {
			s.Path = strings.Join(parts[4:], "/")
		}
	}

	return s, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/import/ -run TestResolveGitHubURL -v`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/import/source.go internal/import/source_test.go
git commit -m "feat(import): add GitHub URL resolver"
```

---

### Task 3: GitHub tarball fetcher

**Files:**
- Create: `internal/import/fetch.go`
- Create: `internal/import/fetch_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/import/fetch_test.go
package skillimport

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTarball creates a tar.gz in memory with the given file map.
// Keys are paths (e.g. "owner-repo-abc123/skills/foo/SKILL.md"), values are content.
func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf strings.Builder
	_ = buf

	pr, pw := io.Pipe()
	go func() {
		gw := gzip.NewWriter(pw)
		tw := tar.NewWriter(gw)
		for name, content := range files {
			hdr := &tar.Header{
				Name: name,
				Mode: 0644,
				Size: int64(len(content)),
			}
			tw.WriteHeader(hdr)
			tw.Write([]byte(content))
		}
		tw.Close()
		gw.Close()
		pw.Close()
	}()
	data, _ := io.ReadAll(pr)
	return data
}

func TestFetch_ExtractsToTempDir(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"anthropics-skills-abc1234/skills/my-skill/SKILL.md": "---\nname: my-skill\n---\nHello",
		"anthropics-skills-abc1234/skills/my-skill/refs/guide.md": "# Guide",
		"anthropics-skills-abc1234/README.md": "# Repo readme",
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

	// Verify files exist in the unpacked directory (tarball root prefix stripped).
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/import/ -run TestFetch -v`
Expected: Compilation error — `NewFetcher` and `Fetcher` don't exist.

- [ ] **Step 3: Write the implementation**

```go
// internal/import/fetch.go
package skillimport

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxTarballSize = 50 << 20 // 50 MB
	fetchTimeout   = 30 * time.Second
)

type Fetcher struct {
	apiBase    string // "https://api.github.com" or test server URL
	githubToken string
	httpClient  *http.Client
}

type FetchResult struct {
	Dir       string // temp directory with unpacked contents (caller must clean up)
	CommitSHA string // extracted from tarball root dir name
}

func NewFetcher(apiBase, githubToken string) *Fetcher {
	return &Fetcher{
		apiBase:    apiBase,
		githubToken: githubToken,
		httpClient: &http.Client{Timeout: fetchTimeout},
	}
}

func (f *Fetcher) Fetch(src Source) (*FetchResult, error) {
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if src.CommitSHA != "" {
		ref = src.CommitSHA
	}

	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", f.apiBase, src.Owner, src.Repo, ref)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build fetch request: %w", err)
	}
	if f.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.githubToken)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	limited := io.LimitReader(resp.Body, maxTarballSize+1)

	tmpDir, err := os.MkdirTemp("", "skael-import-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	commitSHA, err := unpackTarball(limited, tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("unpack tarball: %w", err)
	}

	return &FetchResult{Dir: tmpDir, CommitSHA: commitSHA}, nil
}

func unpackTarball(r io.Reader, destDir string) (string, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var commitSHA string
	var totalSize int64
	var prefix string

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar next: %w", err)
		}

		// GitHub tarballs have a root dir like "owner-repo-shortsha/"
		// Extract the prefix and SHA from the first entry.
		if prefix == "" {
			parts := strings.SplitN(hdr.Name, "/", 2)
			prefix = parts[0] + "/"
			// Extract SHA: last segment after final hyphen
			dashParts := strings.Split(parts[0], "-")
			if len(dashParts) >= 3 {
				commitSHA = dashParts[len(dashParts)-1]
			}
		}

		// Strip the tarball root prefix.
		relPath := strings.TrimPrefix(hdr.Name, prefix)
		if relPath == "" {
			continue
		}

		target := filepath.Join(destDir, filepath.FromSlash(relPath))

		// Path traversal check.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("path traversal: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return "", fmt.Errorf("mkdir %s: %w", relPath, err)
			}
		case tar.TypeReg:
			if hdr.Size > 1<<20 {
				return "", fmt.Errorf("file %s exceeds 1 MiB limit", relPath)
			}
			totalSize += hdr.Size
			if totalSize > maxTarballSize {
				return "", fmt.Errorf("total extraction exceeds %d bytes", maxTarballSize)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", fmt.Errorf("mkdir for %s: %w", relPath, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return "", fmt.Errorf("create %s: %w", relPath, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", fmt.Errorf("write %s: %w", relPath, err)
			}
			f.Close()
		case tar.TypeSymlink, tar.TypeLink:
			return "", fmt.Errorf("rejected %s: symlinks/hardlinks not allowed", relPath)
		default:
			continue
		}
	}

	return commitSHA, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/import/ -run TestFetch -v`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/import/fetch.go internal/import/fetch_test.go
git commit -m "feat(import): add GitHub tarball fetcher"
```

---

### Task 4: Skill discovery — find SKILL.md directories

**Files:**
- Create: `internal/import/discover.go`
- Create: `internal/import/discover_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/import/discover_test.go
package skillimport

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_FindsMultipleSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "code-review", "SKILL.md"),
		"---\nname: code-review\ndescription: Reviews code\n---\nBody")
	writeFile(t, filepath.Join(dir, "skills", "code-review", "references", "guide.md"),
		"# Guide")
	writeFile(t, filepath.Join(dir, "skills", "docx", "SKILL.md"),
		"---\nname: docx\ndescription: Word docs\n---\nBody")
	writeFile(t, filepath.Join(dir, "README.md"), "# Repo")

	results, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d skills, want 2", len(results))
	}

	names := map[string]bool{}
	for _, r := range results {
		names[r.Name] = true
	}
	if !names["code-review"] || !names["docx"] {
		t.Errorf("unexpected names: %v", results)
	}

	for _, r := range results {
		if r.Name == "code-review" {
			if len(r.Files) != 2 {
				t.Errorf("code-review: got %d files, want 2", len(r.Files))
			}
			if r.Description != "Reviews code" {
				t.Errorf("code-review: description = %q", r.Description)
			}
		}
	}
}

func TestDiscover_WithSpecificPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "foo", "SKILL.md"),
		"---\nname: foo\ndescription: Foo skill\n---\nBody")
	writeFile(t, filepath.Join(dir, "skills", "bar", "SKILL.md"),
		"---\nname: bar\ndescription: Bar skill\n---\nBody")

	results, err := Discover(dir, "skills/foo")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d skills, want 1", len(results))
	}
	if results[0].Name != "foo" {
		t.Errorf("name = %q, want %q", results[0].Name, "foo")
	}
}

func TestDiscover_NoSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "# Hello")

	results, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d skills, want 0", len(results))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/import/ -run TestDiscover -v`
Expected: Compilation error — `Discover` doesn't exist.

- [ ] **Step 3: Write the implementation**

```go
// internal/import/discover.go
package skillimport

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

type DiscoveredSkill struct {
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	Path              string           `json:"path"`
	Files             []skill.FileEntry `json:"files"`
	ScanStatus        string           `json:"scan_status"`
	ScanFindingsCount int              `json:"scan_findings_count"`
}

func Discover(rootDir, subPath string) ([]DiscoveredSkill, error) {
	searchDir := rootDir
	if subPath != "" {
		searchDir = filepath.Join(rootDir, filepath.FromSlash(subPath))
	}

	var skillDirs []string

	// If the search dir itself contains SKILL.md, it's a single skill.
	if _, err := os.Stat(filepath.Join(searchDir, "SKILL.md")); err == nil {
		skillDirs = append(skillDirs, searchDir)
	} else {
		// Walk looking for directories that contain SKILL.md.
		filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if info.Name() == "SKILL.md" {
				skillDirs = append(skillDirs, filepath.Dir(path))
			}
			return nil
		})
	}

	var results []DiscoveredSkill
	for _, dir := range skillDirs {
		ds, err := inspectSkillDir(rootDir, dir)
		if err != nil {
			continue
		}
		results = append(results, *ds)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results, nil
}

func inspectSkillDir(rootDir, skillDir string) (*DiscoveredSkill, error) {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return nil, err
	}

	fm, _, err := skill.ParseFrontmatter(string(data))
	if err != nil {
		return nil, err
	}

	name := ""
	description := ""
	if fm != nil {
		if n, ok := fm["name"].(string); ok {
			name = n
		}
		if d, ok := fm["description"].(string); ok {
			description = d
		}
	}
	if name == "" {
		name = filepath.Base(skillDir)
	}

	// Build file list relative to the skill directory.
	var files []skill.FileEntry
	filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return nil
		}
		files = append(files, skill.FileEntry{
			Path: filepath.ToSlash(rel),
			Size: info.Size(),
		})
		return nil
	})

	// Security scan.
	report, scanErr := scan.ScanDir(skillDir)
	scanStatus := "clean"
	scanCount := 0
	if scanErr == nil {
		scanStatus = report.Status
		scanCount = len(report.Findings)
	}

	// Path relative to the root of the fetched repo.
	relPath, _ := filepath.Rel(rootDir, skillDir)

	return &DiscoveredSkill{
		Name:              name,
		Description:       description,
		Path:              filepath.ToSlash(relPath),
		Files:             files,
		ScanStatus:        scanStatus,
		ScanFindingsCount: scanCount,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/import/ -run TestDiscover -v`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/import/discover.go internal/import/discover_test.go
git commit -m "feat(import): add skill directory discovery"
```

---

### Task 5: Import source store — CRUD for provenance

**Files:**
- Create: `internal/import/store.go`
- Create: `internal/import/store_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/import/store_test.go
package skillimport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestStore_UpsertAndGet(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	// Create a skill first to satisfy the FK.
	skillStore := skill.NewStore(pool)
	sk, err := skillStore.Create(ctx, "test-import", "", "test skill", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}

	store := NewStore(pool)

	src := ImportSource{
		SkillID:    sk.ID,
		SourceType: "github",
		SourceURL:  "https://github.com/anthropics/skills",
		SourcePath: "skills/test-import",
		SourceRef:  "main",
		CommitSHA:  "abc123",
	}
	err = store.Upsert(ctx, src)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.GetBySkillID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetBySkillID: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.SourceURL != src.SourceURL {
		t.Errorf("SourceURL = %q, want %q", got.SourceURL, src.SourceURL)
	}
	if got.CommitSHA != "abc123" {
		t.Errorf("CommitSHA = %q, want %q", got.CommitSHA, "abc123")
	}
}

func TestStore_UpsertUpdatesExisting(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	sk, err := skillStore.Create(ctx, "test-reimport", "", "test", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}

	store := NewStore(pool)

	store.Upsert(ctx, ImportSource{SkillID: sk.ID, SourceType: "github", CommitSHA: "aaa"})
	store.Upsert(ctx, ImportSource{SkillID: sk.ID, SourceType: "github", CommitSHA: "bbb"})

	got, _ := store.GetBySkillID(ctx, sk.ID)
	if got.CommitSHA != "bbb" {
		t.Errorf("CommitSHA = %q, want %q (upsert should update)", got.CommitSHA, "bbb")
	}
}

func TestStore_ListAll(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	sk1, _ := skillStore.Create(ctx, "list-a", "", "a", "", json.RawMessage(`{}`))
	sk2, _ := skillStore.Create(ctx, "list-b", "", "b", "", json.RawMessage(`{}`))

	store := NewStore(pool)
	store.Upsert(ctx, ImportSource{SkillID: sk1.ID, SourceType: "github", SourceURL: "https://github.com/a/a"})
	store.Upsert(ctx, ImportSource{SkillID: sk2.ID, SourceType: "local", SourceURL: ""})

	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d, want 2", len(all))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/import/ -run TestStore -v -count=1`
Expected: Compilation error — `NewStore`, `ImportSource`, `Store` don't exist.

- [ ] **Step 3: Write the implementation**

```go
// internal/import/store.go
package skillimport

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

type ImportSource struct {
	ID          string     `json:"id"`
	SkillID     string     `json:"skill_id"`
	SkillName   string     `json:"skill_name,omitempty"`
	SourceType  string     `json:"source_type"`
	SourceURL   string     `json:"source_url"`
	SourcePath  string     `json:"source_path"`
	SourceRef   string     `json:"source_ref"`
	CommitSHA   string     `json:"commit_sha"`
	ImportedAt  time.Time  `json:"imported_at"`
	LastChecked *time.Time `json:"last_checked"`
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Upsert(ctx context.Context, src ImportSource) error {
	const q = `
		INSERT INTO import_sources (skill_id, source_type, source_url, source_path, source_ref, commit_sha)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (skill_id) DO UPDATE SET
			source_type = EXCLUDED.source_type,
			source_url  = EXCLUDED.source_url,
			source_path = EXCLUDED.source_path,
			source_ref  = EXCLUDED.source_ref,
			commit_sha  = EXCLUDED.commit_sha,
			imported_at = now()
	`
	_, err := s.pool.Exec(ctx, q,
		src.SkillID, src.SourceType, src.SourceURL, src.SourcePath, src.SourceRef, src.CommitSHA,
	)
	if err != nil {
		return fmt.Errorf("import.Store.Upsert: %w", err)
	}
	return nil
}

func (s *Store) GetBySkillID(ctx context.Context, skillID string) (*ImportSource, error) {
	const q = `
		SELECT id, skill_id, source_type, source_url, source_path, source_ref, commit_sha, imported_at, last_checked
		FROM import_sources
		WHERE skill_id = $1
	`
	var src ImportSource
	err := s.pool.QueryRow(ctx, q, skillID).Scan(
		&src.ID, &src.SkillID, &src.SourceType, &src.SourceURL,
		&src.SourcePath, &src.SourceRef, &src.CommitSHA, &src.ImportedAt, &src.LastChecked,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("import.Store.GetBySkillID: %w", err)
	}
	return &src, nil
}

func (s *Store) ListAll(ctx context.Context) ([]ImportSource, error) {
	const q = `
		SELECT i.id, i.skill_id, s.name, i.source_type, i.source_url, i.source_path, i.source_ref, i.commit_sha, i.imported_at, i.last_checked
		FROM import_sources i
		JOIN skills s ON s.id = i.skill_id
		ORDER BY i.imported_at DESC
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("import.Store.ListAll query: %w", err)
	}
	defer rows.Close()

	var results []ImportSource
	for rows.Next() {
		var src ImportSource
		if err := rows.Scan(
			&src.ID, &src.SkillID, &src.SkillName, &src.SourceType, &src.SourceURL,
			&src.SourcePath, &src.SourceRef, &src.CommitSHA, &src.ImportedAt, &src.LastChecked,
		); err != nil {
			return nil, fmt.Errorf("import.Store.ListAll scan: %w", err)
		}
		results = append(results, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("import.Store.ListAll rows: %w", err)
	}
	if results == nil {
		results = []ImportSource{}
	}
	return results, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/import/ -run TestStore -v -count=1`
Expected: All tests pass (requires Docker for testcontainers).

- [ ] **Step 5: Commit**

```bash
git add internal/import/store.go internal/import/store_test.go
git commit -m "feat(import): add import source store with upsert"
```

---

### Task 6: Config — add `GITHUB_TOKEN`

**Files:**
- Modify: `internal/platform/config.go`

- [ ] **Step 1: Add GitHubToken to Config struct and LoadConfig**

In `internal/platform/config.go`, add the field to the struct:

```go
type Config struct {
	DatabaseURL   string
	StoragePath   string
	ListenAddr    string
	APIKey        string
	DisableSignup bool
	GitHubToken   string
}
```

And in `LoadConfig`, read it from the environment (optional, no validation needed):

```go
return &Config{
	DatabaseURL:   dbURL,
	APIKey:        apiKey,
	StoragePath:   envDefault("STORAGE_PATH", "./data/skills"),
	ListenAddr:    envDefault("LISTEN_ADDR", ":8080"),
	DisableSignup: os.Getenv("DISABLE_SIGNUP") == "true",
	GitHubToken:   os.Getenv("GITHUB_TOKEN"),
}, nil
```

- [ ] **Step 2: Run existing config tests**

Run: `go test ./internal/platform/ -run TestConfig -v`
Expected: All existing tests pass (new field is optional with zero value).

- [ ] **Step 3: Commit**

```bash
git add internal/platform/config.go
git commit -m "feat(import): add GITHUB_TOKEN to config"
```

---

### Task 7: API routes — resolve, import, upload, sources

**Files:**
- Create: `internal/import/routes.go`
- Create: `internal/import/routes_test.go`
- Modify: `cmd/server/main.go`

This is the largest task. It wires the pipeline into Huma HTTP handlers.

- [ ] **Step 1: Write the route handlers**

```go
// internal/import/routes.go
package skillimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

func RegisterRoutes(api huma.API, router chi.Router, importStore *Store, skillStore *skill.Store, storage *platform.Storage, fetcher *Fetcher) {
	// POST /api/import/resolve — preview skills from a URL
	type resolveBody struct {
		URL string `json:"url" minLength:"1"`
	}
	type resolveInput struct {
		Body resolveBody
	}
	type resolveOutput struct {
		Body struct {
			Source Source            `json:"source"`
			Skills []DiscoveredSkill `json:"skills"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "import-resolve",
		Method:      http.MethodPost,
		Path:        "/api/import/resolve",
		Summary:     "Preview skills available for import from a URL",
	}, func(ctx context.Context, input *resolveInput) (*resolveOutput, error) {
		src, err := ResolveURL(input.Body.URL)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("invalid URL: %v", err))
		}

		result, err := fetcher.Fetch(src)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("fetch failed: %v", err))
		}
		defer os.RemoveAll(result.Dir)

		src.CommitSHA = result.CommitSHA

		skills, err := Discover(result.Dir, src.Path)
		if err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}

		out := &resolveOutput{}
		out.Body.Source = src
		out.Body.Skills = skills
		if out.Body.Skills == nil {
			out.Body.Skills = []DiscoveredSkill{}
		}
		return out, nil
	})

	// POST /api/import — execute import for selected skills
	type importBody struct {
		Source Source   `json:"source"`
		Skills []string `json:"skills" minItems:"1"`
	}
	type importInput struct {
		Body importBody
	}
	type importedSkill struct {
		Name       string `json:"name"`
		Version    int    `json:"version"`
		ScanStatus string `json:"scan_status"`
	}
	type failedSkill struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	type importOutput struct {
		Body struct {
			Imported []importedSkill `json:"imported"`
			Failed   []failedSkill   `json:"failed"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID:   "import-skills",
		Method:        http.MethodPost,
		Path:          "/api/import",
		Summary:       "Import selected skills from a resolved source",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *importInput) (*importOutput, error) {
		src := input.Body.Source

		result, err := fetcher.Fetch(src)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("fetch failed: %v", err))
		}
		defer os.RemoveAll(result.Dir)

		if src.CommitSHA == "" {
			src.CommitSHA = result.CommitSHA
		}

		discovered, err := Discover(result.Dir, src.Path)
		if err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}

		selected := map[string]bool{}
		for _, name := range input.Body.Skills {
			selected[name] = true
		}

		out := &importOutput{}
		out.Body.Imported = []importedSkill{}
		out.Body.Failed = []failedSkill{}

		for _, ds := range discovered {
			if !selected[ds.Name] {
				continue
			}

			ver, err := importSingleSkill(ctx, result.Dir, ds, src, skillStore, importStore, storage)
			if err != nil {
				log.Warn().Err(err).Str("skill", ds.Name).Msg("import failed")
				out.Body.Failed = append(out.Body.Failed, failedSkill{Name: ds.Name, Error: err.Error()})
				continue
			}

			out.Body.Imported = append(out.Body.Imported, importedSkill{
				Name:       ds.Name,
				Version:    ver.Version,
				ScanStatus: ds.ScanStatus,
			})
		}

		return out, nil
	})

	// GET /api/import/sources — list all import provenance
	type sourcesOutput struct {
		Body []ImportSource
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-import-sources",
		Method:      http.MethodGet,
		Path:        "/api/import/sources",
		Summary:     "List all imported skills with source provenance",
	}, func(ctx context.Context, input *struct{}) (*sourcesOutput, error) {
		sources, err := importStore.ListAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("list sources: %w", err)
		}
		return &sourcesOutput{Body: sources}, nil
	})

	// POST /api/import/upload — local upload for CLI
	router.Post("/api/import/upload", makeUploadHandler(skillStore, importStore, storage))
}

func importSingleSkill(
	ctx context.Context,
	rootDir string,
	ds DiscoveredSkill,
	src Source,
	skillStore *skill.Store,
	importStore *Store,
	storage *platform.Storage,
) (*skill.Version, error) {
	skillDir := filepath.Join(rootDir, filepath.FromSlash(ds.Path))

	archive, checksum, manifest, err := skill.Pack(skillDir)
	if err != nil {
		return nil, fmt.Errorf("pack: %w", err)
	}

	// Read SKILL.md for content and frontmatter.
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}
	fm, body, err := skill.ParseFrontmatter(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	var fmJSON json.RawMessage
	if fm != nil {
		fmJSON, _ = json.Marshal(fm)
	} else {
		fmJSON = json.RawMessage(`{}`)
	}

	description := ds.Description
	changelog := ""
	if fm != nil {
		if c, ok := fm["changelog"].(string); ok {
			changelog = c
		}
	}

	// Scan.
	report, err := scan.ScanDir(skillDir)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	scanJSON, _ := json.Marshal(report)

	// Upsert skill record.
	sk, err := skillStore.GetByName(ctx, ds.Name)
	if err != nil {
		return nil, fmt.Errorf("get skill: %w", err)
	}
	if sk == nil {
		sk, err = skillStore.Create(ctx, ds.Name, "", description, body, fmJSON)
		if err != nil {
			return nil, fmt.Errorf("create skill: %w", err)
		}
	}

	// Store archive.
	archiveName := fmt.Sprintf("%s/%s.tar.gz", ds.Name, checksum[:16])
	if _, err := storage.Write(archiveName, bytes.NewReader(archive)); err != nil {
		return nil, fmt.Errorf("store archive: %w", err)
	}

	// Create version.
	ver, err := skillStore.CreateVersion(ctx, sk.ID, archiveName, checksum, changelog, fmJSON, manifest, scanJSON)
	if err != nil {
		_ = storage.Delete(archiveName)
		return nil, fmt.Errorf("create version: %w", err)
	}

	// Update skill metadata (non-fatal, same as publish).
	// Note: UpdateContent takes skill name, not ID.
	skillStore.UpdateContent(ctx, ds.Name, description, body, fmJSON)

	// Record provenance.
	importStore.Upsert(ctx, ImportSource{
		SkillID:    sk.ID,
		SourceType: src.Type,
		SourceURL:  fmt.Sprintf("https://github.com/%s/%s", src.Owner, src.Repo),
		SourcePath: ds.Path,
		SourceRef:  src.Ref,
		CommitSHA:  src.CommitSHA,
	})

	return ver, nil
}

func makeUploadHandler(skillStore *skill.Store, importStore *Store, storage *platform.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 50<<20))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		tmpDir, err := os.MkdirTemp("", "skael-upload-*")
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(tmpDir)

		if err := skill.Unpack(bytes.NewReader(body), tmpDir); err != nil {
			http.Error(w, "invalid archive: "+err.Error(), http.StatusBadRequest)
			return
		}

		skills, err := Discover(tmpDir, "")
		if err != nil {
			http.Error(w, "discover: "+err.Error(), http.StatusInternalServerError)
			return
		}

		type response struct {
			Source Source            `json:"source"`
			Skills []DiscoveredSkill `json:"skills"`
		}
		resp := response{
			Source: Source{Type: "upload"},
			Skills: skills,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
```

- [ ] **Step 2: Wire routes into server main.go**

In `cmd/server/main.go`, after the analytics routes registration (around line 185), add:

```go
// 15. Register import routes.
importStore := skillimport.NewStore(pool)
importFetcher := skillimport.NewFetcher("https://api.github.com", cfg.GitHubToken)
skillimport.RegisterRoutes(api, router, importStore, skillStore, storage, importFetcher)
```

Add the import to the imports block:

```go
skillimport "github.com/skael-dev/skael/internal/import"
```

Also add it to the `--openapi` section (around line 43-55) for spec generation:

```go
skillimport.RegisterRoutes(api, router, nil, nil, nil, nil)
```

- [ ] **Step 3: Verify it compiles and server starts**

Run: `just build && just dev`
Expected: Server starts without errors. New endpoints appear in the OpenAPI spec at `http://localhost:8080/api/openapi.json`.

- [ ] **Step 4: Write integration tests**

```go
// internal/import/routes_test.go
package skillimport

import (
	"testing"
)

func TestResolveURL_Integration(t *testing.T) {
	// Basic smoke test that the URL parser handles edge cases.
	cases := []struct {
		input string
		ok    bool
	}{
		{"https://github.com/anthropics/skills", true},
		{"https://github.com/anthropics/skills/tree/main/skills/docx", true},
		{"github.com/owner/repo", true},
		{"https://gitlab.com/foo/bar", false},
		{"", false},
		{"not-a-url", false},
	}
	for _, c := range cases {
		_, err := ResolveURL(c.input)
		if c.ok && err != nil {
			t.Errorf("ResolveURL(%q) unexpected error: %v", c.input, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ResolveURL(%q) expected error", c.input)
		}
	}
}
```

- [ ] **Step 5: Run all tests**

Run: `go test ./internal/import/ -v -count=1`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/import/routes.go internal/import/routes_test.go cmd/server/main.go
git commit -m "feat(import): add API routes for resolve, import, upload, and sources"
```

---

### Task 8: CLI client methods for import

**Files:**
- Create: `cli/client/import.go`

- [ ] **Step 1: Write the client methods**

```go
// cli/client/import.go
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ImportSource struct {
	Type      string `json:"type"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	CommitSHA string `json:"commit_sha"`
}

type DiscoveredSkill struct {
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	Path              string     `json:"path"`
	Files             []FileEntry `json:"files"`
	ScanStatus        string     `json:"scan_status"`
	ScanFindingsCount int        `json:"scan_findings_count"`
}

type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type ResolveResponse struct {
	Source ImportSource    `json:"source"`
	Skills []DiscoveredSkill `json:"skills"`
}

type ImportedSkill struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	ScanStatus string `json:"scan_status"`
}

type FailedSkill struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type ImportResponse struct {
	Imported []ImportedSkill `json:"imported"`
	Failed   []FailedSkill  `json:"failed"`
}

type ImportSourceEntry struct {
	SkillName  string `json:"skill_name"`
	SourceType string `json:"source_type"`
	SourceURL  string `json:"source_url"`
	SourcePath string `json:"source_path"`
	SourceRef  string `json:"source_ref"`
	CommitSHA  string `json:"commit_sha"`
	ImportedAt string `json:"imported_at"`
}

func (c *Client) ImportResolve(url string) (*ResolveResponse, error) {
	payload, err := json.Marshal(map[string]string{"url": url})
	if err != nil {
		return nil, fmt.Errorf("marshal resolve request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/import/resolve", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode resolve response: %w", err)
	}
	return &result, nil
}

func (c *Client) ImportSkills(source ImportSource, skillNames []string) (*ImportResponse, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"source": source,
		"skills": skillNames,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal import request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/import", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ImportResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode import response: %w", err)
	}
	return &result, nil
}

func (c *Client) ImportUpload(archive []byte) (*ResolveResponse, error) {
	resp, err := c.do(http.MethodPost, "/api/import/upload", bytes.NewReader(archive), "application/gzip")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	return &result, nil
}

func (c *Client) ImportSources() ([]ImportSourceEntry, error) {
	resp, err := c.do(http.MethodGet, "/api/import/sources", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var sources []ImportSourceEntry
	if err := json.Unmarshal(raw, &sources); err != nil {
		return nil, fmt.Errorf("decode sources: %w", err)
	}
	return sources, nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./cli/...`
Expected: Compiles cleanly.

- [ ] **Step 3: Commit**

```bash
git add cli/client/import.go
git commit -m "feat(import): add CLI client methods for import API"
```

---

### Task 9: CLI `skael import` command with Lipgloss styling

**Files:**
- Create: `cli/import.go`

- [ ] **Step 1: Write the import command**

```go
// cli/import.go
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import <url|path>",
	Short: "Import skills from GitHub, local directory, or skills.sh",
	Long: `Import skills into the Skael registry from external sources.

Examples:
  skael import https://github.com/anthropics/skills
  skael import https://github.com/anthropics/skills/tree/main/skills/docx
  skael import ./my-skills/code-review
  skael import --search "react testing"`,
	Args: cobra.MaximumNArgs(1),
	RunE: runImport,
}

var (
	importAll    bool
	importDryRun bool
	importSearch string
)

func init() {
	importCmd.Flags().BoolVar(&importAll, "all", false, "Import all discovered skills without prompting")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "Preview without importing")
	importCmd.Flags().StringVar(&importSearch, "search", "", "Search skills.sh and import from results")
	rootCmd.AddCommand(importCmd)
}

// Styles for the import UI.
var (
	importHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#ededed"))

	importSourceStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#a0a0a0"))

	importNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22c55e")).
			Bold(true)

	importDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a0a0a0"))

	importFilesStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	importBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#333333")).
			Padding(0, 1)

	scanCleanStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22c55e"))

	scanWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f59e0b"))

	scanCriticalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ef4444"))
)

func runImport(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError("not configured", "not_configured", "skael setup <url> <api-key>")
			return nil
		}
		ui.Error(ui.ErrorDetail{
			Message:    "not configured",
			Suggestion: "skael setup <url> <api-key>",
		})
		return nil
	}
	c := client.New(cfg.Endpoint, cfg.APIKey)

	if importSearch != "" {
		return runSearchImport(c, importSearch)
	}

	if len(args) == 0 {
		return fmt.Errorf("provide a URL or local path, or use --search")
	}

	input := args[0]

	// Local path?
	if isLocalPath(input) {
		return runLocalImport(c, input)
	}

	// GitHub URL.
	return runURLImport(c, input)
}

func runURLImport(c *client.Client, rawURL string) error {
	if !ui.JSONMode {
		fmt.Fprintf(os.Stdout, "\n  %s Resolving %s...\n", ui.Accent("↓"), rawURL)
	}

	resolved, err := c.ImportResolve(rawURL)
	if err != nil {
		if ui.JSONMode {
			ui.PrintJSONError(err.Error(), "resolve_error", "")
			return nil
		}
		ui.Errorf("%s", err)
		return nil
	}

	if len(resolved.Skills) == 0 {
		if ui.JSONMode {
			ui.PrintJSONError("no skills found", "no_skills", "")
			return nil
		}
		ui.Warn("No skills found at %s", rawURL)
		return nil
	}

	return presentAndImport(c, resolved)
}

func runLocalImport(c *client.Client, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		ui.Errorf("invalid path: %s", err)
		return nil
	}

	if !ui.JSONMode {
		fmt.Fprintf(os.Stdout, "\n  %s Packing %s...\n", ui.Accent("↓"), absPath)
	}

	// Discover locally first, then pack and upload each skill.
	// If the path itself has a SKILL.md, it's a single skill.
	// Otherwise, discover skill directories within it.
	var dirs []string
	if _, statErr := os.Stat(filepath.Join(absPath, "SKILL.md")); statErr == nil {
		dirs = []string{absPath}
	} else {
		filepath.Walk(absPath, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if info.Name() == "SKILL.md" {
				dirs = append(dirs, filepath.Dir(p))
			}
			return nil
		})
	}

	if len(dirs) == 0 {
		ui.Warn("No skills found in %s", path)
		return nil
	}

	for _, dir := range dirs {
		archive, _, _, err := skill.Pack(dir)
		if err != nil {
			ui.Errorf("pack %s: %s", dir, err)
			continue
		}

		resolved, err := c.ImportUpload(archive)
		if err != nil {
			ui.Errorf("upload %s: %s", dir, err)
			continue
		}

		if len(resolved.Skills) == 0 {
			continue
		}

		// For local imports, import all discovered skills from each archive.
		names := make([]string, len(resolved.Skills))
		for i, s := range resolved.Skills {
			names[i] = s.Name
		}

		result, err := c.ImportSkills(resolved.Source, names)
		if err != nil {
			ui.Errorf("import: %s", err)
			continue
		}

		for _, imp := range result.Imported {
			ui.Success("%s v%d imported", imp.Name, imp.Version)
		}
		for _, fail := range result.Failed {
			ui.Errorf("%s: %s", fail.Name, fail.Error)
		}
	}

	return nil
}

func presentAndImport(c *client.Client, resolved *client.ResolveResponse) error {
	src := resolved.Source
	sourceLabel := fmt.Sprintf("%s/%s", src.Owner, src.Repo)
	refLabel := src.Ref
	if refLabel == "" {
		refLabel = "default"
	}
	shaShort := src.CommitSHA
	if len(shaShort) > 7 {
		shaShort = shaShort[:7]
	}

	if ui.JSONMode {
		if importDryRun {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resolved)
		}
		names := make([]string, len(resolved.Skills))
		for i, s := range resolved.Skills {
			names[i] = s.Name
		}
		result, err := c.ImportSkills(src, names)
		if err != nil {
			ui.PrintJSONError(err.Error(), "import_error", "")
			return nil
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Header.
	fmt.Fprintf(os.Stdout, "\n  %s %s (%s @ %s)\n\n",
		importHeaderStyle.Render("Import ·"),
		importSourceStyle.Render(sourceLabel),
		importSourceStyle.Render(refLabel),
		importSourceStyle.Render(shaShort),
	)

	// Build skill rows.
	var rows []string
	for _, sk := range resolved.Skills {
		check := "[ ]"
		if importAll {
			check = "[x]"
		}

		scanBadge := scanCleanStyle.Render("clean")
		if sk.ScanStatus == "warn" {
			scanBadge = scanWarnStyle.Render("warn")
		} else if sk.ScanStatus == "critical" {
			scanBadge = scanCriticalStyle.Render("critical")
		}

		name := importNameStyle.Render(fmt.Sprintf("%-20s", sk.Name))
		desc := importDescStyle.Render(truncate(sk.Description, 35))
		files := importFilesStyle.Render(fmt.Sprintf("%d files", len(sk.Files)))

		row := fmt.Sprintf("  %s  %s %s  %s  %s", check, name, desc, files, scanBadge)
		rows = append(rows, row)
	}

	fmt.Fprintln(os.Stdout, importBoxStyle.Render(strings.Join(rows, "\n")))

	if importDryRun {
		fmt.Fprintf(os.Stdout, "\n  %s\n\n", importSourceStyle.Render("(dry run — no changes made)"))
		return nil
	}

	// Selection prompt.
	selected := resolved.Skills
	if !importAll {
		fmt.Fprintf(os.Stdout, "\n  %d skills available\n", len(resolved.Skills))
		fmt.Fprintf(os.Stdout, "  Import all? [y/N] ")

		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(os.Stdout, "  Cancelled.")
			return nil
		}
	}

	// Execute import.
	names := make([]string, len(selected))
	for i, s := range selected {
		names[i] = s.Name
	}

	fmt.Fprintf(os.Stdout, "\n  %s Importing %d skills...\n", ui.Accent("↓"), len(names))

	result, err := c.ImportSkills(resolved.Source, names)
	if err != nil {
		ui.Errorf("%s", err)
		return nil
	}

	fmt.Fprintln(os.Stdout)
	for _, imp := range result.Imported {
		ui.Success("%s v%d", imp.Name, imp.Version)
	}
	for _, fail := range result.Failed {
		ui.Errorf("%s: %s", fail.Name, fail.Error)
	}

	parts := []string{fmt.Sprintf("%d imported", len(result.Imported))}
	if len(result.Failed) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", len(result.Failed)))
	}
	ui.Summary(parts...)

	return nil
}

func runSearchImport(c *client.Client, query string) error {
	// skills.sh search is a discovery layer that resolves to GitHub URLs.
	// For now, inform the user this is coming soon.
	ui.Warn("skills.sh search integration is not yet implemented")
	ui.Info("Use a GitHub URL directly: skael import https://github.com/owner/repo")
	return nil
}

func isLocalPath(s string) bool {
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "../") {
		return true
	}
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
```

- [ ] **Step 2: Verify compilation**

Run: `just build`
Expected: Both binaries compile cleanly.

- [ ] **Step 3: Manual test with a real GitHub URL**

Run: `bin/skael import --dry-run https://github.com/anthropics/skills`
Expected: Shows discovered skills with names, descriptions, file counts, and scan badges in styled output. No actual import happens.

- [ ] **Step 4: Commit**

```bash
git add cli/import.go
git commit -m "feat(import): add CLI import command with Lipgloss styling"
```

---

### Task 10: Web UI — import modal

**Files:**
- Create: `web/src/features/import/import-modal.tsx`
- Modify: `web/src/features/skills/skill-list.tsx`

- [ ] **Step 1: Create the import modal component**

```tsx
// web/src/features/import/import-modal.tsx
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, Loader2, AlertTriangle, Check, Package } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { SecurityBadge } from "@/features/security/security-badge";
import { cn } from "@/lib/utils";

type FileEntry = { path: string; size: number };

type DiscoveredSkill = {
  name: string;
  description: string;
  path: string;
  files: FileEntry[];
  scan_status: string;
  scan_findings_count: number;
};

type ImportSource = {
  type: string;
  owner: string;
  repo: string;
  ref: string;
  path: string;
  commit_sha: string;
};

type ResolveResponse = {
  source: ImportSource;
  skills: DiscoveredSkill[];
};

type ImportResult = {
  imported: { name: string; version: number; scan_status: string }[];
  failed: { name: string; error: string }[];
};

async function resolveImport(url: string): Promise<ResolveResponse> {
  const res = await fetch("/api/import/resolve", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.detail || body.title || `Resolve failed (${res.status})`);
  }
  return res.json();
}

async function executeImport(source: ImportSource, skills: string[]): Promise<ImportResult> {
  const res = await fetch("/api/import", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ source, skills }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.detail || body.title || `Import failed (${res.status})`);
  }
  return res.json();
}

type ImportModalProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

export function ImportModal({ open, onOpenChange }: ImportModalProps) {
  const queryClient = useQueryClient();
  const [url, setUrl] = useState("");
  const [resolved, setResolved] = useState<ResolveResponse | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [result, setResult] = useState<ImportResult | null>(null);

  const resolveMutation = useMutation({
    mutationFn: (url: string) => resolveImport(url),
    onSuccess: (data) => {
      setResolved(data);
      setSelected(new Set(data.skills.map((s) => s.name)));
    },
  });

  const importMutation = useMutation({
    mutationFn: ({ source, skills }: { source: ImportSource; skills: string[] }) =>
      executeImport(source, skills),
    onSuccess: (data) => {
      setResult(data);
      queryClient.invalidateQueries({ queryKey: ["analytics"] });
    },
  });

  function handleClose() {
    setUrl("");
    setResolved(null);
    setSelected(new Set());
    setResult(null);
    resolveMutation.reset();
    importMutation.reset();
    onOpenChange(false);
  }

  function toggleSkill(name: string, checked: boolean) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(name);
      else next.delete(name);
      return next;
    });
  }

  const isResolving = resolveMutation.isPending;
  const isImporting = importMutation.isPending;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="sm:max-w-2xl bg-bg-primary border-border">
        <DialogHeader>
          <DialogTitle className="text-text-primary">Import Skills</DialogTitle>
          <DialogDescription className="text-text-tertiary">
            Import skills from a GitHub repository into the registry.
          </DialogDescription>
        </DialogHeader>

        {/* Step 1: URL input */}
        {!resolved && !result && (
          <div className="space-y-3">
            <div className="flex gap-2">
              <input
                type="text"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && url.trim()) resolveMutation.mutate(url.trim());
                }}
                placeholder="https://github.com/owner/repo"
                className="flex-1 h-9 px-3 text-sm bg-bg-secondary border border-border rounded-md text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-border-active"
                disabled={isResolving}
              />
              <Button
                onClick={() => resolveMutation.mutate(url.trim())}
                disabled={!url.trim() || isResolving}
                className="h-9"
              >
                {isResolving ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
                <span className="ml-1.5">{isResolving ? "Resolving..." : "Resolve"}</span>
              </Button>
            </div>
            {resolveMutation.isError && (
              <p className="text-xs text-danger flex items-center gap-1">
                <AlertTriangle size={12} />
                {resolveMutation.error?.message}
              </p>
            )}
          </div>
        )}

        {/* Step 2: Preview + select */}
        {resolved && !result && (
          <div className="space-y-3">
            <div className="text-xs text-text-tertiary">
              {resolved.source.owner}/{resolved.source.repo}
              {resolved.source.ref && ` · ${resolved.source.ref}`}
              {resolved.source.commit_sha && ` @ ${resolved.source.commit_sha.slice(0, 7)}`}
            </div>

            <div className="max-h-[320px] overflow-y-auto border border-border rounded-md divide-y divide-border">
              {resolved.skills.map((sk) => (
                <label
                  key={sk.name}
                  className="flex items-center gap-3 px-3 py-2.5 hover:bg-bg-secondary cursor-pointer transition-colors"
                >
                  <Checkbox
                    checked={selected.has(sk.name)}
                    onCheckedChange={(v) => toggleSkill(sk.name, v === true)}
                    disabled={isImporting}
                  />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-[13px] text-text-primary font-medium">
                        {sk.name}
                      </span>
                      <SecurityBadge status={sk.scan_status} />
                    </div>
                    <p className="text-xs text-text-tertiary truncate">{sk.description}</p>
                  </div>
                  <span className="text-[11px] text-text-tertiary whitespace-nowrap">
                    {sk.files.length} files
                  </span>
                </label>
              ))}
            </div>

            {resolved.skills.length === 0 && (
              <p className="text-sm text-text-tertiary text-center py-4">No skills found in this repository.</p>
            )}

            {importMutation.isError && (
              <p className="text-xs text-danger flex items-center gap-1">
                <AlertTriangle size={12} />
                {importMutation.error?.message}
              </p>
            )}
          </div>
        )}

        {/* Step 3: Result */}
        {result && (
          <div className="space-y-3">
            {result.imported.map((imp) => (
              <div key={imp.name} className="flex items-center gap-2 text-sm">
                <Check size={14} className="text-accent" />
                <span className="font-mono text-text-primary">{imp.name}</span>
                <span className="text-text-tertiary">v{imp.version}</span>
              </div>
            ))}
            {result.failed.map((fail) => (
              <div key={fail.name} className="flex items-center gap-2 text-sm">
                <AlertTriangle size={14} className="text-danger" />
                <span className="font-mono text-text-primary">{fail.name}</span>
                <span className="text-xs text-danger">{fail.error}</span>
              </div>
            ))}
          </div>
        )}

        <DialogFooter>
          {resolved && !result && (
            <Button
              onClick={() =>
                importMutation.mutate({
                  source: resolved.source,
                  skills: Array.from(selected),
                })
              }
              disabled={selected.size === 0 || isImporting}
            >
              {isImporting ? (
                <>
                  <Loader2 size={14} className="animate-spin mr-1.5" />
                  Importing...
                </>
              ) : (
                <>
                  <Package size={14} className="mr-1.5" />
                  Import {selected.size} skill{selected.size !== 1 ? "s" : ""}
                </>
              )}
            </Button>
          )}
          {result && (
            <Button onClick={handleClose}>Done</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 2: Add import button to skill list page**

In `web/src/features/skills/skill-list.tsx`, add the import modal state and button.

At the top of the file, add the import:

```tsx
import { ImportModal } from "@/features/import/import-modal";
```

Inside the `SkillList` component, add state:

```tsx
const [importOpen, setImportOpen] = useState(false);
```

In the header area (near the search bar / bulk actions), add the button and modal:

```tsx
<Button
  onClick={() => setImportOpen(true)}
  variant="outline"
  className="h-8 text-xs"
>
  <Download size={13} className="mr-1.5" />
  Import
</Button>
<ImportModal open={importOpen} onOpenChange={setImportOpen} />
```

Find the exact location in the header by reading the file. Add the button alongside existing header controls.

- [ ] **Step 3: Verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: No type errors.

- [ ] **Step 4: Manual test in browser**

Start the dev server, click the Import button, paste a GitHub URL (e.g. `https://github.com/anthropics/skills`), verify the resolve → preview → import flow works end-to-end.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/import/import-modal.tsx web/src/features/skills/skill-list.tsx
git commit -m "feat(import): add web UI import modal"
```

---

### Task 11: Web UI — import provenance on skill detail

**Files:**
- Modify: `web/src/features/skills/skill-detail.tsx`

- [ ] **Step 1: Add import source fetch and display**

In `skill-detail.tsx`, add a query for the import source. In the `SkillDetail` component, after the existing queries:

```tsx
const importSourceQuery = useQuery({
  queryKey: ["import-source", name],
  queryFn: async () => {
    const res = await fetch(`/api/import/sources`, {
      headers: { "X-API-Key": localStorage.getItem("skael-api-key") ?? "" },
    });
    if (!res.ok) return null;
    const sources: { skill_name: string; source_url: string; source_ref: string; commit_sha: string; imported_at: string }[] = await res.json();
    return sources.find((s) => s.skill_name === name) ?? null;
  },
  enabled: !!name,
});
const importSource = importSourceQuery.data;
```

In the metadata cells area (around lines 767-781), after the "Last updated" MetaCell, add:

```tsx
{importSource && (
  <div className="flex items-center gap-1.5 text-[11px] text-text-tertiary">
    <Download size={11} />
    <span>
      Imported from{" "}
      <a
        href={importSource.source_url}
        target="_blank"
        rel="noopener noreferrer"
        className="text-accent hover:underline"
      >
        {importSource.source_url.replace("https://github.com/", "")}
      </a>
      {importSource.source_ref && ` · ${importSource.source_ref}`}
      {importSource.commit_sha && ` @ ${importSource.commit_sha.slice(0, 7)}`}
    </span>
  </div>
)}
```

Add the `Download` icon to the lucide-react import at the top of the file.

- [ ] **Step 2: Verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: No type errors.

- [ ] **Step 3: Manual test**

Import a skill, then navigate to its detail page. Verify the provenance row appears.

- [ ] **Step 4: Commit**

```bash
git add web/src/features/skills/skill-detail.tsx
git commit -m "feat(import): show import provenance on skill detail page"
```

---

### Task 12: Regenerate OpenAPI SDK

**Files:**
- Modify: `web/src/api/sdk.gen.ts` (auto-generated)
- Modify: `web/src/api/types.gen.ts` (auto-generated)

The new import endpoints need to appear in the generated SDK so the web app can use typed API calls in the future (the import modal currently uses raw `fetch` calls, which is fine for now — the SDK can be adopted later).

- [ ] **Step 1: Regenerate the OpenAPI spec**

Run: `just build` (this rebuilds the server binary with all routes registered)
Then extract the spec: `bin/skael-server --openapi > web/openapi.json`

- [ ] **Step 2: Regenerate the SDK**

Run: `cd web && npx @hey-api/openapi-ts`
Expected: New functions for `importResolve`, `importSkills`, `listImportSources` appear in `sdk.gen.ts`.

- [ ] **Step 3: Commit**

```bash
git add web/src/api/ web/openapi.json
git commit -m "chore: regenerate OpenAPI SDK with import endpoints"
```

---

### Task 13: End-to-end test

**Files:**
- Verify all pieces work together

- [ ] **Step 1: CLI dry-run against real GitHub**

Run: `bin/skael import --dry-run https://github.com/anthropics/skills/tree/main/skills/docx`
Expected: Shows the `docx` skill with its files, scan status, and description. No import happens.

- [ ] **Step 2: CLI import a single skill**

Run: `bin/skael import https://github.com/anthropics/skills/tree/main/skills/docx`
Expected: Skill imported at v1. Appears in `bin/skael list`.

- [ ] **Step 3: CLI import from repo root with selection**

Run: `bin/skael import --all https://github.com/anthropics/skills`
Expected: All discovered skills imported. Each shows success with version number.

- [ ] **Step 4: Web import via modal**

Open the dashboard, click Import, paste `https://github.com/anthropics/skills`, select a skill, import it. Verify it appears in the skill list and the detail page shows provenance.

- [ ] **Step 5: Verify re-import creates new version**

Run: `bin/skael import https://github.com/anthropics/skills/tree/main/skills/docx`
Expected: Imported at v2 (not v1).

- [ ] **Step 6: Run full test suite**

Run: `just test`
Expected: All tests pass including new import package tests.

- [ ] **Step 7: Final commit if any fixups needed**

```bash
git add -A
git commit -m "fix(import): e2e test fixups"
```
