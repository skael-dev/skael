package store_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleSpec() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name:        "pdf-extract",
		Purpose:     "Extract tables.",
		Description: "Extracts tables from PDFs. Use when the user mentions a PDF.",
		Triggers:    []spec.TriggerPhrase{{Text: "extract tables from this pdf"}},
		Steps:       []spec.Step{{ID: "s1", Action: "run it", Postcondition: "out/ exists"}},
		TargetTier:  spec.TierMid,
	}
}

func TestOpen_CreatesLayoutAndIsIdempotent(t *testing.T) {
	root := t.TempDir()

	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, ".whetstone", "whetstone.db")); err != nil {
		t.Errorf("database not created: %v", err)
	}

	// Re-opening an existing store must not fail or reset it.
	s2, err := store.Open(root)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer s2.Close()
}

func TestPaths_EvalSidecarIsOneDirectory(t *testing.T) {
	s := openStore(t)

	// pack strips the sidecar with a single RemoveAll, so the eval set must
	// live under the eval directory.
	eval, err := s.EvalDir("pdf-extract")
	if err != nil {
		t.Fatalf("EvalDir: %v", err)
	}
	suite, err := s.SuiteDir("pdf-extract")
	if err != nil {
		t.Fatalf("SuiteDir: %v", err)
	}
	for _, p := range []string{suite} {
		if !strings.HasPrefix(p, eval+string(filepath.Separator)) {
			t.Errorf("%q is not inside the eval sidecar %q", p, eval)
		}
	}
	// And the sidecar must be inside the skill directory, not beside it.
	skillDir, err := s.SkillDir("pdf-extract")
	if err != nil {
		t.Fatalf("SkillDir: %v", err)
	}
	if !strings.HasPrefix(eval, skillDir+string(filepath.Separator)) {
		t.Errorf("eval dir %q is not inside the skill dir %q", eval, skillDir)
	}
}

func TestEvalDir_UsesTheOneSidecarDefinition(t *testing.T) {
	s := openStore(t)
	dir, err := s.EvalDir("demo")
	if err != nil {
		t.Fatalf("EvalDir: %v", err)
	}
	// The store must not carry its own copy of the sidecar name. lint owns the
	// one definition of what is not shipped bundle content; a second literal
	// here can drift, and a bundle that ships eval scaffolding is the result.
	if filepath.Base(dir) != lint.SidecarDir {
		t.Errorf("EvalDir = %q, want its last element to be lint.SidecarDir (%q)", dir, lint.SidecarDir)
	}
}

func TestPaths_NamespacedNameUsesStrippedDir(t *testing.T) {
	s := openStore(t)
	got, err := s.SkillDir("superpowers:brainstorming")
	if err != nil {
		t.Fatalf("SkillDir: %v", err)
	}
	if filepath.Base(got) != "brainstorming" {
		t.Errorf("SkillDir base = %q, want brainstorming (a colon is not legal in a spec dir name)", filepath.Base(got))
	}
}

func TestPaths_RejectUnsafeNames(t *testing.T) {
	// Names reaching this package may come from a GitHub import or an
	// unpacked archive, not always a validated spec, so every path helper
	// must refuse a bad name itself rather than trust its caller.
	s := openStore(t)
	cases := []string{
		"../../../../../../../../tmp/evalcheck-escape-poc", // path traversal out of the workspace
		"",   // collapses to the shared "skills" parent directory
		":",  // collapses to the shared "skills" parent directory
		"..", // resolves to the workspace root, beside the database
	}
	for _, name := range cases {
		if _, err := s.SkillDir(name); err == nil {
			t.Errorf("SkillDir(%q) succeeded, want a rejection", name)
		}
		if _, err := s.SpecPath(name); err == nil {
			t.Errorf("SpecPath(%q) succeeded, want a rejection", name)
		}
		if _, err := s.EvalDir(name); err == nil {
			t.Errorf("EvalDir(%q) succeeded, want a rejection", name)
		}
		if _, err := s.SuiteDir(name); err == nil {
			t.Errorf("SuiteDir(%q) succeeded, want a rejection", name)
		}
	}
}

func TestSaveSpec_RejectsInvalidName(t *testing.T) {
	s := openStore(t)
	bad := sampleSpec()
	bad.Name = "../../../../../../../../tmp/evalcheck-escape-poc"
	if _, err := s.SaveSpec(bad); err == nil {
		t.Fatal("SaveSpec succeeded with a path-traversal name")
	}

	escaped := filepath.Join(filepath.Dir(filepath.Dir(s.Root())), "tmp", "evalcheck-escape-poc")
	if _, err := os.Stat(escaped); err == nil {
		t.Errorf("SaveSpec created %q outside the workspace", escaped)
	}
}

func TestSaveSpec_VersionsMonotonically(t *testing.T) {
	s := openStore(t)

	v1, err := s.SaveSpec(sampleSpec())
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if v1 != 1 {
		t.Errorf("first version = %d, want 1", v1)
	}

	sp := sampleSpec()
	sp.Purpose = "Extract tables, revised."
	v2, err := s.SaveSpec(sp)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if v2 != 2 {
		t.Errorf("second version = %d, want 2", v2)
	}

	got, v, err := s.LoadSpec("pdf-extract")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if v != 2 {
		t.Errorf("LoadSpec returned version %d, want the latest (2)", v)
	}
	if got.Purpose != "Extract tables, revised." {
		t.Errorf("LoadSpec returned the wrong version's content: %q", got.Purpose)
	}
}

func TestSaveSpec_ConcurrentWritersGetUniqueContiguousVersions(t *testing.T) {
	// A deferred transaction opens its read snapshot at BEGIN and only takes
	// the write lock on its first write; if another writer commits in
	// between, SQLite invalidates the snapshot (SQLITE_BUSY_SNAPSHOT) instead
	// of letting busy_timeout retry it, because there is no lock to wait
	// out — only a stale snapshot. SaveSpec reads MAX(version) before it
	// writes, so it hits exactly that shape without _txlock=immediate.
	s := openStore(t)

	const n = 10
	var wg sync.WaitGroup
	versions := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			versions[i], errs[i] = s.SaveSpec(sampleSpec())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: SaveSpec: %v", i, err)
		}
	}

	seen := make(map[int]bool, n)
	for _, v := range versions {
		if seen[v] {
			t.Errorf("duplicate version %d among %v", v, versions)
		}
		seen[v] = true
	}
	for v := 1; v <= n; v++ {
		if !seen[v] {
			t.Errorf("version %d missing — got %v, want a contiguous 1..%d with no gaps", v, versions, n)
		}
	}
}

func TestSaveSpec_AlsoWritesReadableYAML(t *testing.T) {
	// The approval gate shows a file a human can edit. Storing the spec only as
	// a database blob would make `whetstone spec edit` impossible.
	s := openStore(t)
	if _, err := s.SaveSpec(sampleSpec()); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}

	path, err := s.SpecPath("pdf-extract")
	if err != nil {
		t.Fatalf("SpecPath: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spec yaml not written: %v", err)
	}
	if !strings.Contains(string(b), "name: pdf-extract") {
		t.Errorf("spec yaml looks wrong:\n%s", b)
	}
}

func TestLoadSpec_MissingIsAnError(t *testing.T) {
	s := openStore(t)
	if _, _, err := s.LoadSpec("nope"); err == nil {
		t.Error("LoadSpec succeeded for a skill that was never saved")
	}
}

func TestApproveSpec_IsRecordedPerVersion(t *testing.T) {
	s := openStore(t)
	if _, err := s.SaveSpec(sampleSpec()); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := s.SaveSpec(sampleSpec()); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if err := s.ApproveSpec("pdf-extract", 1); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	hist, err := s.SpecHistory("pdf-extract")
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("history has %d entries, want 2", len(hist))
	}
	// Approving v1 must not approve v2 — a later edit needs re-approval, which
	// is the entire point of the gate.
	var approved int
	for _, r := range hist {
		if r.Approved {
			approved++
			if r.Version != 1 {
				t.Errorf("version %d is approved, want only version 1", r.Version)
			}
		}
	}
	if approved != 1 {
		t.Errorf("%d versions approved, want 1", approved)
	}
}

func TestSpecHistory_MalformedTimestampIsAnError(t *testing.T) {
	// A silently swallowed parse error would return a zero CreatedAt and a
	// nil error — a wrong answer with no signal that anything went wrong.
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.SaveSpec(sampleSpec()); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}

	dbPath := filepath.Join(root, ".whetstone", "whetstone.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE specs SET created_at = ? WHERE skill_name = ?`,
		"2026-07-29T12:00:00Z", "pdf-extract"); err != nil {
		t.Fatalf("corrupt created_at: %v", err)
	}

	if _, err := s.SpecHistory("pdf-extract"); err == nil {
		t.Error("SpecHistory succeeded despite a malformed created_at; the parse error must not be swallowed")
	}
}

func TestCache_RoundTripsAndMisses(t *testing.T) {
	c := openStore(t).Cache()

	if _, ok, err := c.Get("absent"); err != nil || ok {
		t.Errorf("Get(absent) = ok %v, err %v; want false, nil", ok, err)
	}
	if err := c.Put("k", "the response"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get("k")
	if err != nil || !ok {
		t.Fatalf("Get: ok %v, err %v", ok, err)
	}
	if got != "the response" {
		t.Errorf("Get = %q, want %q", got, "the response")
	}
}

func TestCache_PutIsIdempotent(t *testing.T) {
	// A re-run recomputes the same key. An INSERT without upsert would fail on
	// the unique constraint and abort the run.
	c := openStore(t).Cache()
	if err := c.Put("k", "v1"); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := c.Put("k", "v2"); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	got, _, _ := c.Get("k")
	if got != "v2" {
		t.Errorf("Get = %q, want the overwritten value v2", got)
	}
}
