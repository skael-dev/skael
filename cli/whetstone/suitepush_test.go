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

// pushRequestBody is the shape suitepush_test.go decodes a captured request
// body into. Unreviewed carries the field under test below.
type pushRequestBody struct {
	Skill  string `json:"skill"`
	Checks []struct {
		TaskID string `json:"task_id"`
		OK     bool   `json:"ok"`
	} `json:"checks"`
	Archive    string `json:"archive_base64"`
	Unreviewed bool   `json:"unreviewed"`
}

func TestSuitePush_SendsSuiteAndItsChecks(t *testing.T) {
	ws := newWorkspaceWithCheckedSuite(t)
	var got pushRequestBody
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

// TestRunSuitePush_UntouchedSetIsDeclaredUnreviewed pins the whole point of
// the generated-ref record. The generator wrote this set. Nobody opened it.
// The server must record it as machine-derived.
func TestRunSuitePush_UntouchedSetIsDeclaredUnreviewed(t *testing.T) {
	st := newWorkspaceWithCheckedSuite(t)
	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordGeneratedRef("deploy-helper", ref); err != nil {
		t.Fatal(err)
	}

	var captured pushRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ref":"sha256:deadbeef","task_count":1}`))
	}))
	defer srv.Close()

	if err := whetstone.RunSuitePush(context.Background(), whetstone.SuitePushRequest{
		Store: st, Skill: "deploy-helper", Endpoint: srv.URL, APIKey: "k",
	}); err != nil {
		t.Fatalf("RunSuitePush: %v", err)
	}

	if !captured.Unreviewed {
		t.Error("unreviewed = false for an untouched eval set")
	}
}

// TestRunSuitePush_AnEditedSetIsNotDeclaredUnreviewed pins the other half.
// One edit is the act of review. The whole incentive to review rests on
// this branch.
func TestRunSuitePush_AnEditedSetIsNotDeclaredUnreviewed(t *testing.T) {
	st := newWorkspaceWithCheckedSuite(t)
	if err := st.RecordGeneratedRef("deploy-helper", "a-different-ref"); err != nil {
		t.Fatal(err)
	}

	var captured pushRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ref":"sha256:deadbeef","task_count":1}`))
	}))
	defer srv.Close()

	if err := whetstone.RunSuitePush(context.Background(), whetstone.SuitePushRequest{
		Store: st, Skill: "deploy-helper", Endpoint: srv.URL, APIKey: "k",
	}); err != nil {
		t.Fatalf("RunSuitePush: %v", err)
	}

	if captured.Unreviewed {
		t.Error("unreviewed = true for an edited eval set")
	}
}

// TestRunSuitePush_FallsBackWhenTheServerRejectsTheField pins the upgrade
// path. Huma refuses an unknown body property outright, so a server without
// the unreviewed field fails the whole push rather than ignores it. No
// protection exists against such a server.
// A refused push only blocks the author's release.
func TestRunSuitePush_FallsBackWhenTheServerRejectsTheField(t *testing.T) {
	st := newWorkspaceWithCheckedSuite(t)
	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordGeneratedRef("deploy-helper", ref); err != nil {
		t.Fatal(err)
	}

	// A raw map, not pushRequestBody, because the guarantee under test is that
	// the retry drops the "unreviewed" key. A decoded bool reads false both
	// when the key is absent and when it is sent as false. A bool field
	// cannot tell the two cases apart.
	var requests []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		if len(requests) == 1 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":"unexpected property unreviewed"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ref":"sha256:deadbeef","task_count":1}`))
	}))
	defer srv.Close()

	if err := whetstone.RunSuitePush(context.Background(), whetstone.SuitePushRequest{
		Store: st, Skill: "deploy-helper", Endpoint: srv.URL, APIKey: "k",
	}); err != nil {
		t.Fatalf("RunSuitePush: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(requests))
	}
	if _, ok := requests[0]["unreviewed"]; !ok {
		t.Error("first request did not carry the unreviewed key")
	}
	if _, ok := requests[1]["unreviewed"]; ok {
		t.Error("retry still carried the unreviewed key")
	}
}

func TestSuitePush_RefusesWhenSpecCannotBeLoaded(t *testing.T) {
	ws := newWorkspaceWithCheckedSuiteNoSpec(t)
	err := whetstone.RunSuitePush(context.Background(), whetstone.SuitePushRequest{
		Store: ws, Skill: "deploy-helper", Endpoint: "http://unused", APIKey: "k",
	})
	if err == nil {
		t.Fatal("pushed a suite whose spec could not be loaded")
	}
	if !strings.Contains(err.Error(), "deploy-helper") {
		t.Fatalf("error does not name the skill: %v", err)
	}
	if !strings.Contains(err.Error(), "spec") {
		t.Fatalf("error does not say the spec could not be loaded: %v", err)
	}
}

// newWorkspaceWithCheckedSuiteNoSpec writes a checked suite for
// "deploy-helper" the way newWorkspaceWithCheckedSuite does, but never saves a
// spec for it — the state store.LoadSpec sees if a suite/check pair somehow
// outlives its spec (e.g. a workspace copied without the specs table, or a
// spec later purged).
func newWorkspaceWithCheckedSuiteNoSpec(t *testing.T) *store.Store {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	set := &suite.EvalSet{
		SkillName: "deploy-helper",
		Evals: []suite.Eval{
			{ID: 1, Prompt: "Do the thing.", Expectations: []string{"it did the thing"}},
		},
	}
	suiteDir, err := st.SuiteDir("deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if err := suite.WriteEvalSet(suiteDir, set); err != nil {
		t.Fatalf("writing eval set fixture: %v", err)
	}
	return st
}

// newWorkspaceWithCheckedSuite writes a one-task suite for "deploy-helper"
// into a fresh workspace. Nothing records a check any more: suite.Validate is
// pure, so `suite push` runs it itself.
func newWorkspaceWithCheckedSuite(t *testing.T) *store.Store {
	t.Helper()
	return newWorkspaceWithUncheckedSuite(t)
}

// newWorkspaceWithUncheckedSuite writes a suite for "deploy-helper" but
// records nothing else — the state right after `whetstone suite gen`.
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

	set := &suite.EvalSet{
		SkillName: sp.Name,
		Evals: []suite.Eval{
			{ID: 1, Prompt: "Do the thing.", Expectations: []string{"it did the thing"}},
		},
	}
	suiteDir, err := st.SuiteDir(sp.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := suite.WriteEvalSet(suiteDir, set); err != nil {
		t.Fatalf("writing eval set fixture: %v", err)
	}

	return st
}
