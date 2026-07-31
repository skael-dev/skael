package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/spec"
)

// openAtVersion builds a fresh whetstone.db under root with only
// migrations[:n] applied and PRAGMA user_version stamped to n.
//
// store.Open always applies every pending migration, so a database built
// through it is never sitting at a prior schema version — a migration test
// that only ever calls store.Open never actually runs the migration under
// test against a populated older database; it runs against an empty one that
// was already at the target version by the time any row was written. This is
// the only way to get a database at an arbitrary prior version so a test can
// populate it and then exercise store.Open's real upgrade path.
func openAtVersion(t *testing.T, root string, n int) *Store {
	t.Helper()
	if n < 0 || n > len(migrations) {
		t.Fatalf("openAtVersion: n=%d out of range [0,%d]", n, len(migrations))
	}
	base := filepath.Join(root, dirName)
	if err := os.MkdirAll(filepath.Join(base, "skills"), 0o755); err != nil {
		t.Fatalf("openAtVersion: mkdir: %v", err)
	}
	dsn := "file:" + filepath.Join(base, "whetstone.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("openAtVersion: open: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("openAtVersion: apply migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, n)); err != nil {
		t.Fatalf("openAtVersion: stamp version: %v", err)
	}
	return &Store{root: base, db: db}
}

// migrationDemoSpec mirrors store_test.go's demoSpec, duplicated here rather
// than shared because that one lives in the external store_test package and
// this file needs package-internal access to build a pre-migration database.
func migrationDemoSpec() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name:        "demo",
		Purpose:     "Demo skill for store tests.",
		Description: "A minimal skill used to exercise the store.",
		Triggers:    []spec.TriggerPhrase{{Text: "do the demo"}},
		Steps:       []spec.Step{{ID: "s1", Action: "run it", Postcondition: "out/ exists"}},
		TargetTier:  spec.TierMid,
	}
}

// TestMigration3_PreservesAnExistingWorkspace builds a database at version 2
// (only the specs and llm_cache tables exist — no evals/runs/scores/reports
// yet), populates it, and asserts migration 3 (which creates every table
// this package's runtime code reads) applies cleanly on reopen without
// losing what was already there.
func TestMigration3_PreservesAnExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	s := openAtVersion(t, root, 2)
	if _, err := s.SaveSpec(migrationDemoSpec()); err != nil {
		t.Fatal(err)
	}
	if err := s.Cache().Put("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if _, _, err := s2.LoadSpec("demo"); err != nil {
		t.Errorf("spec lost across migration: %v", err)
	}
	if v, ok, err := s2.Cache().Get("k"); err != nil || !ok || v != "v" {
		t.Errorf("cache lost across migration: %q %v %v", v, ok, err)
	}
	if _, err := s2.CreateEval(EvalRecord{Skill: "demo", Tier: "smoke", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Errorf("migration 3 did not apply: %v", err)
	}
}

// TestMigration9_PreservesAnExistingWorkspace builds a database at the
// schema version immediately before the last migration (everything through
// suite_checks, with reports.robustness_gap still NOT NULL DEFAULT 0 and no
// scores.healthy column), populates a report and a score row directly
// against that older shape, then reopens through store.Open and asserts:
//   - the report and eval survive
//   - the DROP COLUMN actually ran: the pre-migration gap value is gone
//     (migration 9's own doc comment says this is expected — the column is
//     dropped and re-added nullable, so old values are not preserved)
//   - the new scores.healthy column exists and defaults to healthy (1) for
//     the pre-existing row
//   - SaveReport/SaveScore work against the new nullable/healthy columns
func TestMigration9_PreservesAnExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	// Version 3: specs, llm_cache, and the big evals/runs/judgments/scores/
	// reports/suite_checks migration, but not the migration under test — the
	// robustness_gap column is still NOT NULL DEFAULT 0 and scores has no
	// healthy column yet. Hardcoded rather than len(migrations)-1: this test
	// is specifically about the transition from version 3 to the next
	// migration, and must fail loudly (a missing table, not a silently wrong
	// version) if that migration is ever removed rather than quietly
	// re-targeting whatever migration happens to be last.
	const preMigration9Version = 3
	s := openAtVersion(t, root, preMigration9Version)

	id, err := s.CreateEval(EvalRecord{Skill: "demo", Tier: "smoke", Status: "running", StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	// Raw inserts, not SaveReport/SaveScore: those functions bind against the
	// *current* schema (a nullable robustness_gap, a healthy column) and would
	// themselves fail or silently target columns that do not exist yet at this
	// older version. The pre-migration shape must be built by hand.
	if _, err := s.db.Exec(
		`INSERT INTO reports (eval_id, doc, headline, panel_complete, robustness_gap) VALUES (?, ?, ?, ?, ?)`,
		id, `{"headline":40}`, 40.0, 1, 12.5); err != nil {
		t.Fatalf("seed pre-migration report: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO scores (eval_id, agent, model, effectiveness) VALUES (?, ?, ?, ?)`,
		id, "claude-code", "opus", 82.0); err != nil {
		t.Fatalf("seed pre-migration score: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	doc, gotID, err := s2.LatestReport("demo")
	if err != nil {
		t.Fatalf("report lost across migration: %v", err)
	}
	if gotID != id || string(doc) != `{"headline":40}` {
		t.Errorf("report content changed across migration: id=%d doc=%s", gotID, doc)
	}

	// The DROP COLUMN really executed: the value written under the old
	// NOT-NULL-DEFAULT-0 column is gone, not carried forward. If migration 9
	// were absent (or a no-op), this would still be 12.5.
	meta, err := s2.ReportMeta(id)
	if err != nil {
		t.Fatalf("ReportMeta: %v", err)
	}
	if meta.RobustnessGap != nil {
		t.Errorf("RobustnessGap = %v after migration 9, want nil: DROP COLUMN should not preserve the pre-migration value", *meta.RobustnessGap)
	}

	rows, err := s2.Scores(id)
	if err != nil {
		t.Fatalf("Scores: %v", err)
	}
	if len(rows) != 1 || !rows[0].Healthy {
		t.Errorf("Scores = %+v, want one row with healthy=true (the ADD COLUMN ... DEFAULT 1 applied to the pre-existing row)", rows)
	}

	// The new nullable/healthy columns must also work going forward.
	gap := 12.5
	if err := s2.SaveReport(id, []byte(`{"headline":40,"robustness_gap":12.5}`), ReportMeta{Headline: 40, PanelComplete: true, RobustnessGap: &gap}); err != nil {
		t.Errorf("SaveReport with a robustness gap failed post-migration: %v", err)
	}
	if err := s2.SaveScore(id, ScoreRow{Agent: "claude-code", Model: "opus", Effectiveness: 82, Healthy: false}); err != nil {
		t.Errorf("SaveScore with the new healthy column failed post-migration: %v", err)
	}
}
