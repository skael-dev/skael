package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
)

func open(t *testing.T) *store.Store {
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
	s := open(t)

	// pack strips the sidecar with a single RemoveAll, so contract and suite
	// must both live under the eval directory.
	eval := s.EvalDir("pdf-extract")
	for _, p := range []string{s.ContractPath("pdf-extract"), s.SuiteDir("pdf-extract")} {
		if !strings.HasPrefix(p, eval+string(filepath.Separator)) {
			t.Errorf("%q is not inside the eval sidecar %q", p, eval)
		}
	}
	// And the sidecar must be inside the skill directory, not beside it.
	if !strings.HasPrefix(eval, s.SkillDir("pdf-extract")+string(filepath.Separator)) {
		t.Errorf("eval dir %q is not inside the skill dir %q", eval, s.SkillDir("pdf-extract"))
	}
}

func TestPaths_NamespacedNameUsesStrippedDir(t *testing.T) {
	s := open(t)
	got := s.SkillDir("superpowers:brainstorming")
	if filepath.Base(got) != "brainstorming" {
		t.Errorf("SkillDir base = %q, want brainstorming (a colon is not legal in a spec dir name)", filepath.Base(got))
	}
}

func TestSaveSpec_VersionsMonotonically(t *testing.T) {
	s := open(t)

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

func TestSaveSpec_AlsoWritesReadableYAML(t *testing.T) {
	// The approval gate shows a file a human can edit. Storing the spec only as
	// a database blob would make `whetstone spec edit` impossible.
	s := open(t)
	if _, err := s.SaveSpec(sampleSpec()); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}

	b, err := os.ReadFile(s.SpecPath("pdf-extract"))
	if err != nil {
		t.Fatalf("spec yaml not written: %v", err)
	}
	if !strings.Contains(string(b), "name: pdf-extract") {
		t.Errorf("spec yaml looks wrong:\n%s", b)
	}
}

func TestLoadSpec_MissingIsAnError(t *testing.T) {
	s := open(t)
	if _, _, err := s.LoadSpec("nope"); err == nil {
		t.Error("LoadSpec succeeded for a skill that was never saved")
	}
}

func TestApproveSpec_IsRecordedPerVersion(t *testing.T) {
	s := open(t)
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

func TestCache_RoundTripsAndMisses(t *testing.T) {
	c := open(t).Cache()

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
	c := open(t).Cache()
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
