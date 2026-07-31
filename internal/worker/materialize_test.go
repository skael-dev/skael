package worker_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/worker"
)

func TestMaterialize_ProducesAWorkspaceThatSatisfiesTheOracleGate(t *testing.T) {
	dir := t.TempDir()
	st, err := worker.Materialize(dir, "deploy-helper", fixtureBundle(t), fixtureSuiteArchive(t),
		[]evalsuite.Check{{TaskID: "t1", OK: true}})
	if err != nil {
		t.Fatal(err)
	}
	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := st.SuiteChecks("deploy-helper", ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) == 0 {
		t.Fatal("the registry's checks were not recorded against the materialized suite ref")
	}
	if _, err := suite.Load(suiteDir); err != nil {
		t.Fatalf("materialized suite does not load: %v", err)
	}
}

// The ref the worker materializes must equal the ref the job asked for, or the
// score it posts is against different tasks than the job names.
func TestMaterialize_RefMatchesTheRequestedRef(t *testing.T) {
	srcDir := t.TempDir()
	writeFixtureSuite(t, srcDir)
	wantRef, err := suite.Ref(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := evalsuite.PackDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	st, err := worker.Materialize(dir, "deploy-helper", fixtureBundle(t), archive,
		[]evalsuite.Check{{TaskID: "t1", OK: true}})
	if err != nil {
		t.Fatal(err)
	}
	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	gotRef, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotRef != wantRef {
		t.Fatalf("materialized suite ref = %s, want %s (the ref the archive was built from)", gotRef, wantRef)
	}
}
