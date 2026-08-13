package worker_test

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/worker"
)

func TestMaterialize_ProducesAWorkspaceThatSatisfiesTheOracleGate(t *testing.T) {
	dir := t.TempDir()
	st, err := worker.Materialize(dir, worker.MaterializeInput{
		Skill: "deploy-helper", Bundle: fixtureBundle(t), SuiteArchive: fixtureSuiteArchive(t),
		Checks: []evalsuite.Check{{TaskID: "t1", OK: true}},
	})
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
	if _, err := suite.LoadEvalSet(suiteDir); err != nil {
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
	st, err := worker.Materialize(dir, worker.MaterializeInput{
		Skill: "deploy-helper", Bundle: fixtureBundle(t), SuiteArchive: archive,
		Checks: []evalsuite.Check{{TaskID: "t1", OK: true}},
	})
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

// A WantSuiteRef that does not match the materialized tree must fail fast,
// before an eval runs against tasks that are not the ones the job named.
func TestMaterialize_FailsFastWhenSuiteRefDoesNotMatchWantSuiteRef(t *testing.T) {
	dir := t.TempDir()
	_, err := worker.Materialize(dir, worker.MaterializeInput{
		Skill: "deploy-helper", Bundle: fixtureBundle(t), SuiteArchive: fixtureSuiteArchive(t),
		Checks:       []evalsuite.Check{{TaskID: "t1", OK: true}},
		WantSuiteRef: "sha256:not-the-real-ref",
	})
	if err == nil {
		t.Fatal("Materialize accepted a suite whose ref did not match WantSuiteRef")
	}
	if !strings.Contains(err.Error(), "sha256:not-the-real-ref") {
		t.Fatalf("error does not name the mismatched ref: %v", err)
	}
}

// When a spec is provided, Materialize must use it rather than falling back
// to a placeholder reconstructed from SKILL.md frontmatter — the fallback
// loses the skill's real deps and purpose.
func TestMaterialize_UsesTheProvidedSpecInsteadOfReconstructing(t *testing.T) {
	dir := t.TempDir()
	sp := &spec.SkillSpec{
		Name:        "deploy-helper",
		Purpose:     "deploy things to production, carefully",
		Description: "Deploys the thing.",
		Triggers:    []spec.TriggerPhrase{{Text: "deploy the thing"}},
		Steps:       []spec.Step{{ID: "s1", Action: "deploy", Postcondition: "deployed"}},
		Deps:        spec.DepsDecl{Apt: []string{"curl"}},
		TargetTier:  spec.TierMid,
	}

	st, err := worker.Materialize(dir, worker.MaterializeInput{
		Skill: "deploy-helper", Bundle: fixtureBundle(t), SuiteArchive: fixtureSuiteArchive(t),
		Checks: []evalsuite.Check{{TaskID: "t1", OK: true}}, Spec: sp,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := st.LoadSpec("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Purpose != sp.Purpose {
		t.Fatalf("stored spec purpose = %q, want the provided spec's %q (fell back to reconstruction instead of using it)", loaded.Purpose, sp.Purpose)
	}
	if len(loaded.Deps.Apt) != 1 || loaded.Deps.Apt[0] != "curl" {
		t.Fatalf("stored spec deps = %+v, want the provided spec's apt:[curl]", loaded.Deps)
	}
}
