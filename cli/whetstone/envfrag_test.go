package whetstone_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

func TestEval_RefusesATaskWithADockerfileFragment(t *testing.T) {
	deps := fakeEvalDeps(t)
	writeSuiteWithEnvFrag(t, deps.Store, "deploy-helper", "t3", "RUN apt-get install -y pandoc")

	_, err := whetstone.RunEvalWith(context.Background(), deps, whetstone.EvalRequest{
		Skill: "deploy-helper", Tier: runner.TierSmoke,
	})
	if err == nil {
		t.Fatal("an eval ran with a task fragment that would have been ignored")
	}
	if !strings.Contains(err.Error(), "t3") {
		t.Fatalf("the error does not name the task: %v", err)
	}
	if !strings.Contains(err.Error(), "environment/Dockerfile.frag") {
		t.Fatalf("the error does not tell the author what to remove: %v", err)
	}
}

func TestSuiteCheck_RefusesATaskWithADockerfileFragment(t *testing.T) {
	tmpDir := t.TempDir()

	// Change to the temporary directory for this test
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	// store.Open expects the root directory (which contains .whetstone), not the .whetstone directory itself
	st, err := store.Open(tmpDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	writeSuiteWithEnvFrag(t, st, "deploy-helper", "t3", "RUN apt-get install -y pandoc")

	err = whetstone.RunSuiteCheck(context.Background(), "deploy-helper", false)
	if err == nil {
		t.Fatal("suite check ran with a task fragment that would have been ignored")
	}
	if !strings.Contains(err.Error(), "t3") {
		t.Fatalf("the error does not name the task: %v", err)
	}
	if !strings.Contains(err.Error(), "environment/Dockerfile.frag") {
		t.Fatalf("the error does not tell the author what to remove: %v", err)
	}
}

// fakeEvalDeps creates a minimal EvalDeps for testing without Docker or network.
func fakeEvalDeps(t *testing.T) whetstone.EvalDeps {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedSkill(t, st, "deploy-helper", 5, nil)

	adapter := &fakeAdapter{}
	adapters := func(name string) (agent.Adapter, bool) {
		if name == "claude-code" {
			return adapter, true
		}
		return nil, false
	}

	return whetstone.EvalDeps{
		Store:         st,
		Driver:        &fakeDriver{},
		Gateway:       newScriptedGateway(t),
		Adapters:      adapters,
		Now:           func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) },
		Sleep:         func(time.Duration) {},
		EngineVersion: "test-engine",
	}
}

// writeSuiteWithEnvFrag creates an approved spec, suite, and suite checks
// for a skill, with a specific task that has an EnvFrag.
func writeSuiteWithEnvFrag(t *testing.T, st *store.Store, skillName, taskID, envFrag string) {
	t.Helper()

	// Create the spec
	sp := &spec.SkillSpec{
		Name:        skillName,
		Purpose:     "Do the thing.",
		Description: "Does the thing, for testing.",
		Triggers:    []spec.TriggerPhrase{{Text: "do the thing"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/do.py", Postcondition: "out/done.txt exists"},
		},
		TargetTier: spec.TierMid,
	}
	if errs := sp.Validate(); len(errs) > 0 {
		t.Fatalf("invalid fixture spec: %v", errs)
	}
	version, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if err := st.ApproveSpec(skillName, version); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	// Compile and save the contract
	c, err := contract.Compile(sp)
	if err != nil {
		t.Fatalf("contract.Compile: %v", err)
	}
	contractPath, err := st.ContractPath(skillName)
	if err != nil {
		t.Fatalf("ContractPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cf, err := os.Create(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(cf); err != nil {
		t.Fatal(err)
	}
	if err := cf.Close(); err != nil {
		t.Fatal(err)
	}

	// Create the suite with the specified task having an EnvFrag
	s := &suite.Suite{}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("t%d", i)
		task := suite.TaskPkg{
			ID:       id,
			Kind:     "happy",
			PromptMD: fmt.Sprintf("Do the thing for task %d.", i),
			Oracle:   "#!/bin/sh\ntrue\n",
			Verifier: "#!/bin/sh\nexit 0\n",
		}
		if id == taskID {
			task.EnvFrag = envFrag
		}
		s.Tasks = append(s.Tasks, task)
	}

	suiteDir, err := st.SuiteDir(skillName)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(suiteDir); err != nil {
		t.Fatalf("writing suite fixture: %v", err)
	}

	// Save suite checks for all tasks
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]store.SuiteCheckRow, len(s.Tasks))
	for i, task := range s.Tasks {
		rows[i] = store.SuiteCheckRow{TaskID: task.ID}
	}
	if err := st.SaveSuiteCheck(skillName, ref, rows); err != nil {
		t.Fatalf("SaveSuiteCheck: %v", err)
	}

	// Write the skill directory with a minimal SKILL.md
	bundleDir, err := st.SkillDir(skillName)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"), []byte("---\nname: "+skillName+"\n---\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEval_AllowsAFilteredOutTaskWithADockerfileFragment(t *testing.T) {
	// Smoke tier needs 5 tasks. Create a suite with 10 tasks where t7 has EnvFrag.
	// Filter to include t0-t4, which excludes the broken task entirely.
	deps := fakeEvalDeps(t)
	st := deps.Store

	// Create spec and contract (same as writeSuiteWithEnvFrag but with 10 tasks)
	sp := &spec.SkillSpec{
		Name:        "deploy-helper",
		Purpose:     "Do the thing.",
		Description: "Does the thing, for testing.",
		Triggers:    []spec.TriggerPhrase{{Text: "do the thing"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/do.py", Postcondition: "out/done.txt exists"},
		},
		TargetTier: spec.TierMid,
	}
	version, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if err := st.ApproveSpec("deploy-helper", version); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	c, err := contract.Compile(sp)
	if err != nil {
		t.Fatalf("contract.Compile: %v", err)
	}
	contractPath, err := st.ContractPath("deploy-helper")
	if err != nil {
		t.Fatalf("ContractPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cf, err := os.Create(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(cf); err != nil {
		t.Fatal(err)
	}
	if err := cf.Close(); err != nil {
		t.Fatal(err)
	}

	// Create suite with 10 tasks, t7 has EnvFrag
	s := &suite.Suite{}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("t%d", i)
		task := suite.TaskPkg{
			ID:       id,
			Kind:     "happy",
			PromptMD: fmt.Sprintf("Do the thing for task %d.", i),
			Oracle:   "#!/bin/sh\ntrue\n",
			Verifier: "#!/bin/sh\nexit 0\n",
		}
		if id == "t7" {
			task.EnvFrag = "RUN apt-get install -y pandoc"
		}
		s.Tasks = append(s.Tasks, task)
	}

	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(suiteDir); err != nil {
		t.Fatalf("writing suite fixture: %v", err)
	}

	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]store.SuiteCheckRow, len(s.Tasks))
	for i, task := range s.Tasks {
		rows[i] = store.SuiteCheckRow{TaskID: task.ID}
	}
	if err := st.SaveSuiteCheck("deploy-helper", ref, rows); err != nil {
		t.Fatalf("SaveSuiteCheck: %v", err)
	}

	bundleDir, err := st.SkillDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"), []byte("---\nname: deploy-helper\n---\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Filter to include t0-t4 (5 tasks for smoke tier), excluding t7 which has EnvFrag
	_, err = whetstone.RunEvalWith(context.Background(), deps, whetstone.EvalRequest{
		Skill:      "deploy-helper",
		Tier:       runner.TierSmoke,
		TaskFilter: []string{"t0", "t1", "t2", "t3", "t4"}, // deliberately exclude t7
	})
	if err != nil {
		t.Fatalf("filtered-out task with EnvFrag blocked the eval: %v", err)
	}
}

func TestEval_RefusesAFilteredInTaskWithADockerfileFragment(t *testing.T) {
	deps := fakeEvalDeps(t)
	st := deps.Store

	// Create spec and contract
	sp := &spec.SkillSpec{
		Name:        "deploy-helper",
		Purpose:     "Do the thing.",
		Description: "Does the thing, for testing.",
		Triggers:    []spec.TriggerPhrase{{Text: "do the thing"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/do.py", Postcondition: "out/done.txt exists"},
		},
		TargetTier: spec.TierMid,
	}
	version, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if err := st.ApproveSpec("deploy-helper", version); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	c, err := contract.Compile(sp)
	if err != nil {
		t.Fatalf("contract.Compile: %v", err)
	}
	contractPath, err := st.ContractPath("deploy-helper")
	if err != nil {
		t.Fatalf("ContractPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cf, err := os.Create(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(cf); err != nil {
		t.Fatal(err)
	}
	if err := cf.Close(); err != nil {
		t.Fatal(err)
	}

	// Create suite with 10 tasks, t7 has EnvFrag
	s := &suite.Suite{}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("t%d", i)
		task := suite.TaskPkg{
			ID:       id,
			Kind:     "happy",
			PromptMD: fmt.Sprintf("Do the thing for task %d.", i),
			Oracle:   "#!/bin/sh\ntrue\n",
			Verifier: "#!/bin/sh\nexit 0\n",
		}
		if id == "t7" {
			task.EnvFrag = "RUN apt-get install -y pandoc"
		}
		s.Tasks = append(s.Tasks, task)
	}

	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(suiteDir); err != nil {
		t.Fatalf("writing suite fixture: %v", err)
	}

	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]store.SuiteCheckRow, len(s.Tasks))
	for i, task := range s.Tasks {
		rows[i] = store.SuiteCheckRow{TaskID: task.ID}
	}
	if err := st.SaveSuiteCheck("deploy-helper", ref, rows); err != nil {
		t.Fatalf("SaveSuiteCheck: %v", err)
	}

	bundleDir, err := st.SkillDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"), []byte("---\nname: deploy-helper\n---\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Filter to include t3-t7 (5 tasks) where t7 has EnvFrag — should fail
	_, err = whetstone.RunEvalWith(context.Background(), deps, whetstone.EvalRequest{
		Skill:      "deploy-helper",
		Tier:       runner.TierSmoke,
		TaskFilter: []string{"t3", "t4", "t5", "t6", "t7"}, // t7 with EnvFrag is included
	})
	if err == nil {
		t.Fatal("an eval ran with a filtered-in task that has a fragment")
	}
	if !strings.Contains(err.Error(), "t7") {
		t.Fatalf("the error does not name the task: %v", err)
	}
}
