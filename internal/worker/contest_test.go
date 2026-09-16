package worker_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/worker"
)

func contestJob(suiteRef string) evalqueue.Job {
	return evalqueue.Job{
		ID: "job-contest", ContestID: "contest-1",
		SkillID: "s1", SkillName: "alpha", Version: 2, SuiteRef: suiteRef, Tier: "smoke",
		Candidates: []evalqueue.Candidate{
			{SkillID: "s1", SkillName: "alpha", Version: 2, Label: "alpha@2"},
			{SkillID: "s2", SkillName: "beta", Version: 5, Label: "beta@5"},
		},
	}
}

// Every candidate must run inside the one claim, against the one suite. Two
// claims mean two workers, two CLI versions and two days, which is the drift
// the contest exists to remove.
func TestRunContest_RunsEveryCandidateAgainstOneSuite(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(contestJob(ref))
	r := &fakeRunner{reportSuiteRef: ref}

	w := newTestWorker(t, api, r, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if api.contestReported != "contest-1" {
		t.Errorf("reported contest %q, want contest-1", api.contestReported)
	}
	if len(api.contestReports) != 2 {
		t.Fatalf("reported %d candidate reports, want 2", len(api.contestReports))
	}
	for _, label := range []string{"alpha@2", "beta@5"} {
		rep, ok := api.contestReports[label]
		if !ok {
			t.Fatalf("no report for %s", label)
		}
		if rep.SuiteRef != ref {
			t.Errorf("%s ran against suite %q, want the contest's %q", label, rep.SuiteRef, ref)
		}
	}
}

// A contest with no suite derives one from every candidate, never from the
// first: a suite derived from one candidate grades the others against that
// candidate's claims.
func TestRunContest_DerivesFromEveryCandidate(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	api.enqueue(contestJob(""))
	api.pushRef = ref
	der := &fakeDeriver{result: &worker.DeriveResult{Archive: api.suiteArchive, Tasks: 12}}
	r := &fakeRunner{reportSuiteRef: ref}

	w := newTestWorker(t, api, r, der)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	der.mu.Lock()
	defer der.mu.Unlock()
	if der.contestCandidates != 2 {
		t.Errorf("derived from %d candidates, want 2", der.contestCandidates)
	}
}

// A contest job carrying one candidate is a verdict over a single entry.
func TestRunContest_RefusesASingleCandidate(t *testing.T) {
	api := newFakeAPI(t)
	ref := fixtureSuiteRef(t)
	job := contestJob(ref)
	job.Candidates = job.Candidates[:1]
	api.enqueue(job)

	w := newTestWorker(t, api, &fakeRunner{reportSuiteRef: ref}, &fakeDeriver{})
	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce accepted a one-candidate contest")
	}
	if api.failCause == "" {
		t.Error("the job was not failed on the server")
	}
	if api.contestReported != "" {
		t.Error("a verdict was reported over a single entry")
	}
}
