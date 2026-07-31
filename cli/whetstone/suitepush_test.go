package whetstone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

func TestSuitePush_SendsSuiteAndItsChecks(t *testing.T) {
	ws := newWorkspaceWithCheckedSuite(t)
	var got struct {
		Skill  string `json:"skill"`
		Checks []struct {
			TaskID string `json:"task_id"`
			OK     bool   `json:"ok"`
		} `json:"checks"`
		Archive string `json:"archive_base64"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ref":"sha256:deadbeef","task_count":1}`))
	}))
	defer srv.Close()

	if err := whetstone.RunSuitePush(context.Background(), whetstone.SuitePushRequest{
		Store: ws, Skill: "deploy-helper", Endpoint: srv.URL, APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}
	if got.Skill != "deploy-helper" || len(got.Checks) == 0 || got.Archive == "" {
		t.Fatalf("request body was incomplete: %+v", got)
	}
}

func TestSuitePush_RefusesWhenNoCheckIsRecorded(t *testing.T) {
	ws := newWorkspaceWithUncheckedSuite(t)
	err := whetstone.RunSuitePush(context.Background(), whetstone.SuitePushRequest{
		Store: ws, Skill: "deploy-helper", Endpoint: "http://unused", APIKey: "k",
	})
	if err == nil {
		t.Fatal("pushed a suite with no recorded check")
	}
	if !strings.Contains(err.Error(), "suite check") {
		t.Fatalf("error does not tell the author what to run: %v", err)
	}
}

// newWorkspaceWithCheckedSuite writes a one-task suite for "deploy-helper"
// into a fresh workspace and records a passing suite check for it, mirroring
// what `whetstone suite gen` followed by `whetstone suite check` leaves
// behind.
func newWorkspaceWithCheckedSuite(t *testing.T) *store.Store {
	t.Helper()
	st := newWorkspaceWithUncheckedSuite(t)

	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := []store.SuiteCheckRow{{TaskID: "t00"}}
	if err := st.SaveSuiteCheck("deploy-helper", ref, rows); err != nil {
		t.Fatalf("SaveSuiteCheck: %v", err)
	}
	return st
}

// newWorkspaceWithUncheckedSuite writes a suite for "deploy-helper" but
// records no suite check for it — the state right after `whetstone suite
// gen`, before `whetstone suite check` has run.
func newWorkspaceWithUncheckedSuite(t *testing.T) *store.Store {
	t.Helper()

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
			{
				ID:       "t00",
				Kind:     "happy",
				PromptMD: "Do the thing.",
				Oracle:   "#!/bin/sh\ntrue\n",
				Verifier: "#!/bin/sh\nexit 0\n",
			},
		},
	}
	suiteDir, err := st.SuiteDir(sp.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(suiteDir); err != nil {
		t.Fatalf("writing suite fixture: %v", err)
	}

	return st
}
