package worker_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/worker"
)

// A materialized workspace must carry a compiled drift contract.
//
// RunEvalWith opens Store.ContractPath at its scoring step — after the whole
// panel has already executed in the sandbox. A workspace without a contract
// therefore does not fail fast: it burns a full panel run and only then dies
// with "opening contract for X: no such file or directory (run `whetstone new
// X` first)", advice that makes no sense on a worker, which has no authored
// workspace to run `whetstone new` in. Materialize is the only place the
// worker can produce it, and contract.Compile is a pure function of the spec
// Materialize already holds, so nothing needs to travel over the wire for it.
func TestMaterialize_WritesTheCompiledContract(t *testing.T) {
	dir := t.TempDir()
	st, err := worker.Materialize(dir, worker.MaterializeInput{
		Skill: "deploy-helper", Bundle: fixtureBundle(t), SuiteArchive: fixtureSuiteArchive(t),
		Checks: []evalsuite.Check{{TaskID: "t1", OK: true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	path, err := st.ContractPath("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}

	// Open and Load exactly as RunEvalWith's scoring step does.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("materialized workspace has no contract, so every eval this worker runs "+
			"fails at scoring after the panel has already run: %v", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := contract.Load(f); err != nil {
		t.Fatalf("materialized contract does not load: %v", err)
	}
}

// The skill under test must actually be present in the workspace.
//
// RunEvalWith passes Store.SkillDir to the runner as BundleDir, and the agent
// adapter copies that whole tree into the sandbox as the installed skill. If
// the downloaded bundle is unpacked somewhere else, that directory holds only
// spec.yaml and the eval sidecar: the panel runs with no SKILL.md, measures a
// skill that was never installed, and the run is scored as though the skill
// itself had failed. That is worse than an error, because it completes and
// posts a real-looking score.
func TestMaterialize_PutsTheBundleWhereTheRunnerLooksForIt(t *testing.T) {
	dir := t.TempDir()
	st, err := worker.Materialize(dir, worker.MaterializeInput{
		Skill: "deploy-helper", Bundle: fixtureBundle(t), SuiteArchive: fixtureSuiteArchive(t),
		Checks: []evalsuite.Check{{TaskID: "t1", OK: true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// SkillDir is exactly what RunEvalWith hands the runner as BundleDir.
	skillDir, err := st.SkillDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("the dir the runner installs as the skill has no SKILL.md, so the panel "+
			"evaluates a skill that is not there: %v", err)
	}
}
