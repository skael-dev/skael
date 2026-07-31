package skillimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// fakeQueue is a minimal evalqueue.Executor that records every Submit call,
// optionally failing every one of them, so a test can assert on both
// "did it enqueue" and "did an enqueue failure abort anything else".
type fakeQueue struct {
	calls []evalqueue.Job
	err   error
}

func (f *fakeQueue) Submit(ctx context.Context, j evalqueue.Job) (evalqueue.JobID, error) {
	f.calls = append(f.calls, j)
	if f.err != nil {
		return "", f.err
	}
	return evalqueue.JobID("job-1"), nil
}

func (f *fakeQueue) Cancel(ctx context.Context, id evalqueue.JobID) error {
	return nil
}

// writeFixtureSuite mirrors internal/evalsuite/registry_test.go's helper of
// the same name: a minimal suite tree that suite.Load accepts. The prompt
// embeds skillName so two different skills' fixture suites pack to distinct
// content — Put's ref is content-addressed (ON CONFLICT (ref) DO NOTHING),
// so identical archives for two skills would silently collide onto one row
// keyed to whichever skill Put first.
func writeFixtureSuite(t *testing.T, dir, skillName string) {
	t.Helper()
	s := &suite.Suite{
		Tasks: []suite.TaskPkg{
			{
				ID:       "t1",
				Kind:     "happy",
				Split:    "holdout",
				PromptMD: "# Task for " + skillName + "\n\nDo the thing.\n",
				Oracle:   "#!/bin/sh\necho ok\n",
				Verifier: "#!/bin/sh\nexit 0\n",
			},
		},
		Triggers: suite.TriggerSet{
			Positive: []string{"do the thing"},
			Negative: []string{"do something unrelated"},
		},
	}
	require.NoError(t, s.Write(dir))
}

// registerFixtureSuite registers a suite for skillName against reg, so
// importSingleSkill's LatestForSkill lookup finds it.
func registerFixtureSuite(t *testing.T, reg *evalsuite.Registry, skillName string) {
	t.Helper()
	dir := t.TempDir()
	writeFixtureSuite(t, dir, skillName)
	archive, err := evalsuite.PackDir(dir)
	require.NoError(t, err)
	_, err = reg.Put(context.Background(), skillName, archive, []evalsuite.Check{{TaskID: "t1", OK: true}}, 1, "test")
	require.NoError(t, err)
}

// writeFixtureSkillDir writes a minimal, valid SKILL.md under rootDir/name.
func writeFixtureSkillDir(t *testing.T, rootDir, name string) DiscoveredSkill {
	t.Helper()
	skillDir := filepath.Join(rootDir, name)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillMD := "---\nname: " + name + "\ndescription: a fixture skill\n---\n\n# " + name + "\n\nBody content.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644))
	return DiscoveredSkill{Name: name, Description: "a fixture skill", Path: name}
}

func TestImportSingleSkill_EnqueuesOneJobWhenASuiteIsRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	importStore := NewStore(pool)
	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suites := evalsuite.NewRegistry(pool, suiteStorage)
	registerFixtureSuite(t, suites, "deploy-helper")

	rootDir := t.TempDir()
	ds := writeFixtureSkillDir(t, rootDir, "deploy-helper")
	src := Source{Type: "github", Owner: "acme", Repo: "skills", Ref: "main", CommitSHA: "abc123"}

	q := &fakeQueue{}
	ver, created, err := importSingleSkill(ctx, rootDir, ds, src, skillStore, importStore, storage, nil, q, suites)
	require.NoError(t, err)
	require.True(t, created)

	if len(q.calls) != 1 {
		t.Fatalf("Submit called %d times, want exactly 1", len(q.calls))
	}
	job := q.calls[0]
	if job.RequestedBy != "import" {
		t.Fatalf("RequestedBy = %q, want %q", job.RequestedBy, "import")
	}
	if job.Version != ver.Version {
		t.Fatalf("job.Version = %d, want %d (the version import just created)", job.Version, ver.Version)
	}
	if job.SkillName != "deploy-helper" {
		t.Fatalf("job.SkillName = %q, want %q", job.SkillName, "deploy-helper")
	}
}

func TestImportSingleSkill_NoSuiteMeansNoJob(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	importStore := NewStore(pool)
	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suites := evalsuite.NewRegistry(pool, suiteStorage)
	// deliberately no registerFixtureSuite call

	rootDir := t.TempDir()
	ds := writeFixtureSkillDir(t, rootDir, "deploy-helper")
	src := Source{Type: "github", Owner: "acme", Repo: "skills", Ref: "main", CommitSHA: "abc123"}

	q := &fakeQueue{}
	_, created, err := importSingleSkill(ctx, rootDir, ds, src, skillStore, importStore, storage, nil, q, suites)
	require.NoError(t, err)
	require.True(t, created)

	if len(q.calls) != 0 {
		t.Fatalf("Submit called %d times, want 0 — no suite means no job", len(q.calls))
	}
}

// A Submit failure for one skill must not abort the rest of the batch: the
// version and archive for that skill are already durable by the time the
// queue is touched, and later skills in the same import must still be
// attempted. importSingleSkill itself doesn't loop — RegisterRoutes's
// handler does — so this proves the unit contract that makes that possible:
// an enqueue failure is swallowed (logged) rather than returned as an error
// that would land the skill in the "failed" bucket for a queue reason it had
// nothing to do with.
func TestImportSingleSkill_SubmitFailureDoesNotAbortTheImport(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	skillStore := skill.NewStore(pool)
	importStore := NewStore(pool)
	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suites := evalsuite.NewRegistry(pool, suiteStorage)
	registerFixtureSuite(t, suites, "skill-a")
	registerFixtureSuite(t, suites, "skill-b")

	rootDir := t.TempDir()
	dsA := writeFixtureSkillDir(t, rootDir, "skill-a")
	dsB := writeFixtureSkillDir(t, rootDir, "skill-b")
	src := Source{Type: "github", Owner: "acme", Repo: "skills", Ref: "main", CommitSHA: "abc123"}

	failing := &fakeQueue{err: context.DeadlineExceeded}

	_, createdA, errA := importSingleSkill(ctx, rootDir, dsA, src, skillStore, importStore, storage, nil, failing, suites)
	if errA != nil {
		t.Fatalf("skill-a: enqueue failure must not fail the import: %v", errA)
	}
	if !createdA {
		t.Fatal("skill-a: expected created=true even though enqueue failed")
	}

	_, createdB, errB := importSingleSkill(ctx, rootDir, dsB, src, skillStore, importStore, storage, nil, failing, suites)
	if errB != nil {
		t.Fatalf("skill-b: a prior enqueue failure must not abort subsequent skills: %v", errB)
	}
	if !createdB {
		t.Fatal("skill-b: expected created=true — the batch must continue after skill-a's enqueue failure")
	}

	if len(failing.calls) != 2 {
		t.Fatalf("Submit called %d times, want 2 (one attempt per skill, neither aborted)", len(failing.calls))
	}
}
