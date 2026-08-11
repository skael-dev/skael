package evalsuite_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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

// writeFixtureSuite writes a minimal eval set to dir that suite.LoadEvalSet
// accepts: one eval with a prompt and an expectation.
func writeFixtureSuite(t *testing.T, dir string) {
	t.Helper()
	set := &suite.EvalSet{
		SkillName: "demo",
		Evals: []suite.Eval{
			{ID: 1, Prompt: "Do the thing.", Expectations: []string{"it did the thing"}},
		},
	}
	if err := suite.WriteEvalSet(dir, set); err != nil {
		t.Fatalf("writeFixtureSuite: %v", err)
	}
	if err := suite.WriteTriggerQueries(dir, []suite.TriggerQuery{
		{Query: "do the thing", ShouldTrigger: true},
		{Query: "do something unrelated"},
	}); err != nil {
		t.Fatalf("writeFixtureSuite triggers: %v", err)
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

// newTestRegistry returns a Registry over a fresh test database and local
// storage, plus the pool so a test can write in the same transaction shape a
// production caller (e.g. the report handler) would use.
func newTestRegistry(t *testing.T) (*evalsuite.Registry, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.SetupTestDB(t)
	return evalsuite.NewRegistry(pool, newTempStorage(t)), pool
}

// putFixtureSuite pushes a minimal fixture suite for skillName and returns
// the stored record.
func putFixtureSuite(t *testing.T, reg *evalsuite.Registry, skillName string) *evalsuite.Record {
	t.Helper()
	archive := fixtureSuiteArchive(t)
	checks := []evalsuite.Check{{TaskID: "t1", OK: true}}
	rec, err := reg.Put(ctx, skillName, archive, checks, 1, "nate@example.com", nil)
	if err != nil {
		t.Fatalf("putFixtureSuite: %v", err)
	}
	return rec
}

func TestRegistry_PutRecordsAuthoredOrigin(t *testing.T) {
	// A suite pushed through the normal path is authored until something
	// says otherwise.
	reg, _ := newTestRegistry(t)
	rec := putFixtureSuite(t, reg, "demo")
	if rec.Origin != evalsuite.OriginAuthored {
		t.Fatalf("Put recorded origin %q, want %q", rec.Origin, evalsuite.OriginAuthored)
	}
}

func TestRegistry_MarkDerived(t *testing.T) {
	reg, pool := newTestRegistry(t)
	rec := putFixtureSuite(t, reg, "demo")

	if err := reg.MarkDerived(context.Background(), pool, rec.Ref); err != nil {
		t.Fatalf("MarkDerived: %v", err)
	}

	got, err := reg.Get(context.Background(), rec.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Origin != evalsuite.OriginDerived {
		t.Fatalf("origin %q after MarkDerived, want %q", got.Origin, evalsuite.OriginDerived)
	}
}

func TestRegistry_MarkDerivedUnknownRefIsNotFound(t *testing.T) {
	// A silent no-op here would leave a derived suite classified as authored,
	// which is the one direction that matters.
	reg, pool := newTestRegistry(t)
	err := reg.MarkDerived(context.Background(), pool, "no-such-ref")
	if !errors.Is(err, evalsuite.ErrNotFound) {
		t.Fatalf("MarkDerived on unknown ref returned %v, want ErrNotFound", err)
	}
}

func TestRegistry_PutIsIdempotentOnRef(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	st := newTempStorage(t)
	reg := evalsuite.NewRegistry(pool, st)
	archive := fixtureSuiteArchive(t) // packs testdata/suite
	checks := []evalsuite.Check{{TaskID: "t1", OK: true}}

	a, err := reg.Put(ctx, "deploy-helper", archive, checks, 1, "nate@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := reg.Put(ctx, "deploy-helper", archive, checks, 1, "nate@example.com", nil)
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

// A caller that passes the JSON literal null as specJSON (e.g. a Go nil
// marshaled through encoding/json, rather than an actually-omitted field)
// must not have that literal stored in the spec column: read back later, it
// would unmarshal into a non-nil, empty *SkillSpec instead of being treated
// as "no spec recorded" — see internal/worker.unmarshalSuiteSpec.
func TestRegistry_NormalizesJSONNullSpecToSQLNull(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	st := newTempStorage(t)
	reg := evalsuite.NewRegistry(pool, st)
	archive := fixtureSuiteArchive(t)
	checks := []evalsuite.Check{{TaskID: "t1", OK: true}}

	rec, err := reg.Put(ctx, "deploy-helper", archive, checks, 1, "nate@example.com", []byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Spec) != 0 {
		t.Fatalf("rec.Spec = %q, want empty (JSON null must normalize to SQL NULL, not be stored literally)", rec.Spec)
	}

	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT spec FROM eval_suites WHERE ref = $1`, rec.Ref).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Fatalf("spec column = %q, want SQL NULL", raw)
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
	rec, err := reg.Put(ctx, "deploy-helper", archive, []evalsuite.Check{{TaskID: "t1", OK: true}}, 1, "nate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Ref != local {
		t.Fatalf("registry ref %s != suite.Ref %s", rec.Ref, local)
	}
}

func TestRegistry_RejectsASuiteWithNoChecks(t *testing.T) {
	reg := evalsuite.NewRegistry(testutil.SetupTestDB(t), newTempStorage(t))
	_, err := reg.Put(ctx, "deploy-helper", fixtureSuiteArchive(t), nil, 1, "nate", nil)
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
	rec, err := reg.Put(ctx, "deploy-helper", archive, []evalsuite.Check{{TaskID: "t1", OK: true}}, 1, "nate", nil)
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
	if _, err := suite.LoadEvalSet(out); err != nil {
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
