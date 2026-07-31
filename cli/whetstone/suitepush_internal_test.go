package whetstone

import (
	"context"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// TestRunSuitePush_RefusesAnOversizedArchive shrinks maxArchiveBytes to a
// value the fixture suite's packed archive clears, so the guard can be
// exercised without generating a multi-megabyte fixture on every test run.
func TestRunSuitePush_RefusesAnOversizedArchive(t *testing.T) {
	orig := maxArchiveBytes
	maxArchiveBytes = 1 // anything real will exceed this
	t.Cleanup(func() { maxArchiveBytes = orig })

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

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
	if errs := sp.Validate(); len(errs) > 0 {
		t.Fatalf("invalid fixture spec: %v", errs)
	}
	version, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if err := st.ApproveSpec(sp.Name, version); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	s := &suite.Suite{
		Tasks: []suite.TaskPkg{
			{ID: "t00", Kind: "happy", PromptMD: "Do the thing.", Oracle: "#!/bin/sh\ntrue\n", Verifier: "#!/bin/sh\nexit 0\n"},
		},
	}
	suiteDir, err := st.SuiteDir(sp.Name)
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
	if err := st.SaveSuiteCheck(sp.Name, ref, []store.SuiteCheckRow{{TaskID: "t00"}}); err != nil {
		t.Fatalf("SaveSuiteCheck: %v", err)
	}

	err = RunSuitePush(context.Background(), SuitePushRequest{
		Store: st, Skill: sp.Name, Endpoint: "http://unused", APIKey: "k",
	})
	if err == nil {
		t.Fatal("pushed an archive over the size limit")
	}
	if !strings.Contains(err.Error(), "too large") && !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error does not say the suite is too large: %v", err)
	}
}
