package evalsuite_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

var ctx = context.Background()

// newTempStorage returns a platform.LocalStorage rooted at a fresh temp dir.
func newTempStorage(t *testing.T) *platform.LocalStorage {
	t.Helper()
	st, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("newTempStorage: %v", err)
	}
	return st
}

// writeFixtureSuite writes a minimal suite tree to dir that suite.Load
// accepts: one task with a prompt, oracle, and verifier.
func writeFixtureSuite(t *testing.T, dir string) {
	t.Helper()
	s := &suite.Suite{
		Tasks: []suite.TaskPkg{
			{
				ID:       "t1",
				Kind:     "happy",
				Split:    "holdout",
				PromptMD: "# Task\n\nDo the thing.\n",
				Oracle:   "#!/bin/sh\necho ok\n",
				Verifier: "#!/bin/sh\nexit 0\n",
			},
		},
		Triggers: suite.TriggerSet{
			Positive: []string{"do the thing"},
			Negative: []string{"do something unrelated"},
		},
	}
	if err := s.Write(dir); err != nil {
		t.Fatalf("writeFixtureSuite: %v", err)
	}
}

// fixtureSuiteArchive packs a freshly written fixture suite into an archive.
func fixtureSuiteArchive(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	writeFixtureSuite(t, dir)
	archive, err := evalsuite.PackDir(dir)
	if err != nil {
		t.Fatalf("fixtureSuiteArchive: %v", err)
	}
	return archive
}

// archiveWithSymlink builds a tar.gz containing a single symlink entry.
func archiveWithSymlink(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name:     "evil-link",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
		Mode:     0o777,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("archiveWithSymlink write header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("archiveWithSymlink close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("archiveWithSymlink close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestRegistry_PutIsIdempotentOnRef(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	st := newTempStorage(t)
	reg := evalsuite.NewRegistry(pool, st)
	archive := fixtureSuiteArchive(t) // packs testdata/suite
	checks := []evalsuite.Check{{TaskID: "t1", OK: true}}

	a, err := reg.Put(ctx, "deploy-helper", archive, checks, 1, "nate@example.com")
	if err != nil {
		t.Fatal(err)
	}
	b, err := reg.Put(ctx, "deploy-helper", archive, checks, 1, "nate@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if a.Ref != b.Ref {
		t.Fatalf("same content produced two refs: %s and %s", a.Ref, b.Ref)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM eval_suites WHERE ref = $1`, a.Ref).Scan(&n)
	if n != 1 {
		t.Fatalf("rows for ref = %d, want 1", n)
	}
}

// The ref must be the content hash of the extracted suite, i.e. the same value
// suite.Ref computes locally — otherwise a report's suite_ref can never match
// the registry's and every score is unattributable.
func TestRegistry_RefMatchesSuiteRefOfTheExtractedTree(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	reg := evalsuite.NewRegistry(pool, newTempStorage(t))
	dir := t.TempDir()
	writeFixtureSuite(t, dir)
	local, err := suite.Ref(dir)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := evalsuite.PackDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := reg.Put(ctx, "deploy-helper", archive, []evalsuite.Check{{TaskID: "t1", OK: true}}, 1, "nate")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Ref != local {
		t.Fatalf("registry ref %s != suite.Ref %s", rec.Ref, local)
	}
}

func TestRegistry_RejectsASuiteWithNoChecks(t *testing.T) {
	reg := evalsuite.NewRegistry(testutil.SetupTestDB(t), newTempStorage(t))
	_, err := reg.Put(ctx, "deploy-helper", fixtureSuiteArchive(t), nil, 1, "nate")
	if err == nil {
		t.Fatal("a suite with no oracle-gate results was accepted")
	}
	if !strings.Contains(err.Error(), "suite check") {
		t.Fatalf("error does not name the missing check: %v", err)
	}
}

func TestRegistry_RoundTripsThroughFetchAndUnpack(t *testing.T) {
	reg := evalsuite.NewRegistry(testutil.SetupTestDB(t), newTempStorage(t))
	dir := t.TempDir()
	writeFixtureSuite(t, dir)
	archive, _ := evalsuite.PackDir(dir)
	rec, err := reg.Put(ctx, "deploy-helper", archive, []evalsuite.Check{{TaskID: "t1", OK: true}}, 1, "nate")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Fetch(ctx, rec.Ref)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := evalsuite.Unpack(got, out); err != nil {
		t.Fatal(err)
	}
	back, err := suite.Ref(out)
	if err != nil {
		t.Fatal(err)
	}
	if back != rec.Ref {
		t.Fatalf("ref after round trip = %s, want %s", back, rec.Ref)
	}
	if _, err := suite.Load(out); err != nil {
		t.Fatalf("unpacked tree does not load as a suite: %v", err)
	}
}

func TestUnpack_RejectsASymlinkEntry(t *testing.T) {
	archive := archiveWithSymlink(t) // tar.gz containing one symlink entry
	err := evalsuite.Unpack(archive, t.TempDir())
	if err == nil {
		t.Fatal("a symlink entry was extracted")
	}
}
